//go:build linux && !dae_stub_ebpf

package dae

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
)

// A reachable-but-silent UDP upstream must not consume the core close-tail
// deadline and kill the management process during OFF/reload.
func TestIsolatedDNSBlackholeShutdown(t *testing.T) {
	if os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" {
		t.Skip("disposable kernel only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("disposable root CI required")
	}
	upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	global := `global { disable_waiting_network: true bpf_conn_state_map_size: 2048 }`
	dnsText := fmt.Sprintf(`dns {
 bind: 'tcp+udp://127.0.0.1:15353'
 ipversion_prefer: 4
 upstream { fixture: 'udp://%s' }
 routing { request { fallback: fixture } response { fallback: accept } }
 }`, upstream.LocalAddr())
	routing := `routing { fallback: direct }`
	conf, err := ParseConfig(&global, &dnsText, &routing)
	if err != nil {
		t.Fatal(err)
	}
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	r, err := startRuntime(log, conf, nil)
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		q := new(dns.Msg)
		q.SetQuestion("blackhole.acceptance.invalid.", dns.TypeAAAA)
		_, _, _ = (&dns.Client{Timeout: 15 * time.Second}).Exchange(q, "127.0.0.1:15353")
	}()
	_ = upstream.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err = upstream.ReadFrom(make([]byte, 4096)); err != nil {
		_ = r.close()
		t.Fatal("query did not reach silent upstream", err)
	}
	started := time.Now()
	if err = r.close(); err != nil {
		t.Fatal("OFF failed with outstanding DNS", err)
	}
	if time.Since(started) > 15*time.Second {
		t.Fatal("shutdown exceeded bound")
	}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("query survived shutdown")
	}
	// Reopening both DNS transports proves ownership was released.
	udp, err := net.ListenPacket("udp", "127.0.0.1:15353")
	if err != nil {
		t.Fatal(err)
	}
	udp.Close()
	tcp, err := net.Listen("tcp", "127.0.0.1:15353")
	if err != nil {
		t.Fatal(err)
	}
	tcp.Close()
}
