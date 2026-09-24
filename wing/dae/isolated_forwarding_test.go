//go:build linux && !dae_stub_ebpf

package dae

import (
	"bufio"
	"fmt"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// A synthetic LAN client lives in its own namespace. Success/block/OFF checks
// use new TCP and UDP sockets, rather than inferring forwarding from DNS/API.
func TestIsolatedLANForwarding(t *testing.T) {
	if os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" {
		t.Skip("disposable kernel only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("disposable root CI required")
	}
	ns := fmt.Sprintf("bridge-client-%d", os.Getpid())
	ip := func(args ...string) {
		t.Helper()
		if out, e := exec.Command("ip", args...).CombinedOutput(); e != nil {
			t.Fatalf("ip %v: %v %s", args, e, out)
		}
	}
	ip("netns", "add", ns)
	defer exec.Command("ip", "netns", "delete", ns).Run()
	ip("link", "add", "brtest0", "type", "veth", "peer", "name", "brpeer0")
	defer exec.Command("ip", "link", "del", "brtest0").Run()
	ip("link", "set", "brpeer0", "netns", ns)
	ip("addr", "add", "192.0.2.1/24", "dev", "brtest0")
	ip("link", "set", "brtest0", "up")
	ip("-n", ns, "addr", "add", "192.0.2.2/24", "dev", "brpeer0")
	ip("-n", ns, "link", "set", "brpeer0", "up")
	ip("-n", ns, "link", "set", "lo", "up")
	// Keep the target off-host: DAE intentionally bypasses local UDP sockets.
	// A host-local echo would test that exemption, not forwarded LAN policy.
	serverNS := ns + "-server"
	ip("netns", "add", serverNS)
	defer exec.Command("ip", "netns", "delete", serverNS).Run()
	ip("link", "add", "brwan0", "type", "veth", "peer", "name", "brserver0")
	defer exec.Command("ip", "link", "del", "brwan0").Run()
	ip("link", "set", "brserver0", "netns", serverNS)
	ip("addr", "add", "198.18.0.1/24", "dev", "brwan0")
	ip("link", "set", "brwan0", "up")
	ip("-n", serverNS, "addr", "add", "198.18.0.2/24", "dev", "brserver0")
	ip("-n", serverNS, "link", "set", "brserver0", "up")
	ip("-n", serverNS, "link", "set", "lo", "up")
	ip("-n", serverNS, "route", "add", "default", "via", "198.18.0.1")
	ip("-n", ns, "route", "add", "default", "via", "192.0.2.1")
	if e := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644); e != nil {
		t.Fatal(e)
	}
	server := exec.Command("ip", "netns", "exec", serverNS, "python3", "-u", "-c", `
import socket,threading
s=socket.socket();s.bind(('198.18.0.2',18080));s.listen()
u=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);u.bind(('198.18.0.2',18080))
def udp():
 while True:
  b,a=u.recvfrom(65535);u.sendto(b,a)
def conn(c):
 c.settimeout(2)
 try:
  while True:
   b=c.recv(65535)
   if not b: break
   c.sendall(b)
 finally: c.close()
threading.Thread(target=udp,daemon=True).start()
print('ready',flush=True)
while True:
 c,a=s.accept();threading.Thread(target=conn,args=(c,),daemon=True).start()
`)
	stdout, e := server.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	server.Stderr = os.Stderr
	if e = server.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(stdout); ready <- scanner.Scan() && scanner.Text() == "ready" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("echo server not ready")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("echo server startup timeout")
	}
	check := func(want string) {
		t.Helper()
		script := `
import socket,sys
want=sys.argv[1]
for kind in (socket.SOCK_STREAM,socket.SOCK_DGRAM):
 s=socket.socket(socket.AF_INET,kind);s.settimeout(0.7)
 passed=False
 try:
  s.connect(('198.18.0.2',18080));s.sendall(b'bridge-probe');passed=s.recv(32)==b'bridge-probe'
 except OSError: pass
 finally: s.close()
 if passed != (want=='allow'): raise SystemExit('unexpected '+str(kind)+' result '+str(passed))
print('TCP/UDP '+want)
`
		out, e := exec.Command("ip", "netns", "exec", ns, "python3", "-c", script, want).CombinedOutput()
		if e != nil {
			t.Fatalf("forwarding %s: %v %s", want, e, out)
		}
	}
	stress := func(label string) {
		t.Helper()
		script := `
import socket,sys,time,concurrent.futures,json
label=sys.argv[1]
def probe(i):
 kind=socket.SOCK_STREAM if i%2==0 else socket.SOCK_DGRAM
 payload=bytes((j%251 for j in range(65536 if kind==socket.SOCK_STREAM else 1200)))
 start=time.monotonic()
 with socket.socket(socket.AF_INET,kind) as s:
  s.settimeout(3);s.connect(('198.18.0.2',18080));s.sendall(payload)
  if kind==socket.SOCK_STREAM:
   s.shutdown(socket.SHUT_WR);received=b''
   while True:
    b=s.recv(65536)
    if not b: break
    received+=b
  else: received=s.recv(65535)
  if received!=payload: raise RuntimeError('payload/half-close mismatch')
 return (time.monotonic()-start)*1000
for workers in (1,8,32,128):
 with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
  results=sorted(pool.map(probe,range(max(32,workers*2))))
 print(json.dumps(dict(route=label,workers=workers,count=len(results),failures=0,p50_ms=results[len(results)//2],p95_ms=results[int(len(results)*.95)],p99_ms=results[min(len(results)-1,int(len(results)*.99))])),flush=True)
`
		cmd := exec.Command("ip", "netns", "exec", ns, "python3", "-c", script, label)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("%s pressure: %v %s", label, e, out)
		}
		t.Log(string(out))
	}
	glob := `global {
 disable_waiting_network: true
 bpf_conn_state_map_size: 2048
 lan_interface: brtest0
 auto_config_kernel_parameter: true
 }`
	route := `routing { fallback: direct }`
	direct, e := ParseConfig(&glob, nil, &route)
	if e != nil {
		t.Fatal(e)
	}
	blockedRoute := strings.Replace(route, "direct", "block", 1)
	blocked, e := ParseConfig(&glob, nil, &blockedRoute)
	if e != nil {
		t.Fatal(e)
	}
	// The ingress synchronizer supplies hosts overrides. Exercise the same
	// interface with two synthetic names, inside this private mount namespace.
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if e := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n198.18.0.1 ingress-a.acceptance.invalid ingress-b.acceptance.invalid\n198.18.0.2 allowed.acceptance.invalid\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("mount", "--bind", hostsPath, "/etc/hosts").CombinedOutput(); e != nil {
		t.Fatalf("isolated hosts mount: %v %s", e, out)
	}
	defer exec.Command("umount", "/etc/hosts").Run()
	// Fixed synthetic SS group; direct success alone cannot satisfy this phase.
	ssTCP, e := net.Listen("tcp", "198.18.0.1:19090")
	if e != nil {
		t.Fatal(e)
	}
	defer ssTCP.Close()
	ssUDP, e := net.ListenPacket("udp", "198.18.0.1:19090")
	if e != nil {
		t.Fatal(e)
	}
	defer ssUDP.Close()
	var accepted, udpProbes atomic.Int64
	go func() {
		for {
			c, e := ssTCP.Accept()
			if e != nil {
				return
			}
			accepted.Add(1)
			go serveShadowsocksAeadConn(t, c, "aes-128-gcm", "synthetic-acceptance-only")
		}
	}()
	go serveShadowsocksAeadUDP(t, ssUDP, "aes-128-gcm", "synthetic-acceptance-only", &udpProbes)
	proxy := *direct
	proxy.Node = []daeConfig.KeyableString{"ss://YWVzLTEyOC1nY206c3ludGhldGljLWFjY2VwdGFuY2Utb25seQ@ingress-a.acceptance.invalid:19090#fixture"}
	proxy.Group = []daeConfig.Group{{Name: "fixture", Policy: []*config_parser.Function{{Name: "fixed", Params: []*config_parser.Param{{Val: "0"}}}}}}
	proxy.Routing.Fallback = "fixture"
	requests := make(chan *ReloadMessage)
	done := make(chan error, 1)
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	go func() { done <- runCoordinator(log, EmptyConfig, nil, requests) }()
	defer func() {
		close(requests)
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(30 * time.Second):
			t.Error("shutdown stalled")
		}
	}()
	reload := func(msg *ReloadMessage) {
		t.Helper()
		reply := make(chan error, 1)
		msg.Callback = reply
		select {
		case requests <- msg:
		case <-time.After(30 * time.Second):
			t.Fatal("send stalled")
		}
		select {
		case e := <-reply:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("reload stalled")
		}
	}
	check("allow")
	for i := 0; i < 3; i++ {
		t.Logf("LAN cycle %d direct/SS/block/OFF", i+1)
		reload(&ReloadMessage{Config: direct})
		check("allow")
		if i == 0 {
			stress("direct")
		}
		if i == 1 {
			proxy.Node[0] = daeConfig.KeyableString(strings.Replace(string(proxy.Node[0]), "ingress-a.", "ingress-b.", 1))
		}
		before := accepted.Load()
		beforeUDP := udpProbes.Load()
		reload(&ReloadMessage{Config: &proxy})
		check("allow")
		if accepted.Load() <= before || udpProbes.Load() <= beforeUDP {
			t.Fatal("proxy test bypassed SS TCP server")
		}
		if i == 0 {
			stress("ss")
			if os.Getenv("DAED_ACCEPTANCE_SOAK") == "1" {
				sample := func() [4]int {
					t.Helper()
					fds, e := os.ReadDir("/proc/self/fd")
					if e != nil {
						t.Fatal(e)
					}
					b, e := os.ReadFile("/proc/self/status")
					if e != nil {
						t.Fatal(e)
					}
					v := [4]int{len(fds), runtime.NumGoroutine(), 0, 0}
					for _, line := range strings.Split(string(b), "\n") {
						f := strings.Fields(line)
						if len(f) > 1 {
							switch f[0] {
							case "Threads:":
								v[2], _ = strconv.Atoi(f[1])
							case "VmRSS:":
								v[3], _ = strconv.Atoi(f[1])
							}
						}
					}
					return v
				}
				start := time.Now()
				nextLog := time.Minute
				count := 0
				var plateau [4]int
				for time.Since(start) < 10*time.Minute {
					check("allow")
					count++
					if plateau[0] == 0 && time.Since(start) >= 7*time.Minute {
						runtime.GC()
						plateau = sample()
					}
					if time.Since(start) >= nextLog {
						t.Logf("SS soak elapsed=%s probes=%d fd/goroutines/threads/rssKiB=%v", time.Since(start).Round(time.Second), count, sample())
						nextLog += time.Minute
					}
					time.Sleep(time.Second)
				}
				runtime.GC()
				end := sample()
				if end[0] > plateau[0]+64 || end[1] > plateau[1]+128 || end[2] > plateau[2]+16 || end[3] > plateau[3]+65536 {
					t.Fatalf("SS soak resource plateau exceeded: minute7=%v end=%v", plateau, end)
				}
				t.Logf("10-minute SS soak PASS: %d fresh TCP/UDP pairs; minute7=%v final=%v", count, plateau, end)
			}
			domainText := `routing {
                sip(192.0.2.2/32) && domain(suffix:allowed.acceptance.invalid) -> fixture
                sip(192.0.2.2/32) -> block
                fallback: direct
            }`
			parsed, e := ParseConfig(&glob, nil, &domainText)
			if e != nil {
				t.Fatal(e)
			}
			domainConf := proxy
			domainConf.Routing = parsed.Routing
			reload(&ReloadMessage{Config: &domainConf})
			for _, host := range []string{"allowed.acceptance.invalid", "blocked.acceptance.invalid"} {
				script := `
import socket,sys
host=sys.argv[1];p=('GET / HTTP/1.1\r\nHost: '+host+'\r\nConnection: close\r\n\r\n').encode()
s=socket.socket();s.settimeout(1);ok=False
try:
 s.connect(('198.18.0.2',18080));s.sendall(p);b=b''
 while len(b)<len(p):
  more=s.recv(4096)
  if not more: break
  b+=more
 ok=b==p
except OSError: pass
finally:s.close()
if ok!=(host.startswith('allowed.')):raise SystemExit('domain policy mismatch '+host+' '+str(ok))
`
				if out, e := exec.Command("ip", "netns", "exec", ns, "python3", "-c", script, host).CombinedOutput(); e != nil {
					t.Fatalf("domain routing: %v %s", e, out)
				}
			}
			t.Log("device/domain whitelist proxy and default block verified against the same destination IP")
			reload(&ReloadMessage{Config: &proxy})
		}
		if i == 2 {
			_ = ssTCP.Close()
			_ = ssUDP.Close()
			check("block")
		}
		reload(&ReloadMessage{Config: blocked})
		check("block")
		reload(&ReloadMessage{Config: EmptyConfig})
		check("allow")
	}
	t.Log("real LAN veth TCP/UDP direct/block/OFF recovery passed; synthetic SS TCP/UDP included; no provider interoperability claim")
}
