//go:build linux && !dae_stub_ebpf

package dae

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
)

func TestIsolatedRuntimeCoordinator(t *testing.T) {
	if os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" {
		t.Skip("disposable kernel acceptance only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("disposable root CI required")
	}
	upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: upstream, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		r := new(dns.Msg)
		r.SetReply(q)
		if q.Question[0].Name == "missing.acceptance.invalid." {
			r.Rcode = dns.RcodeNameError
			soa, _ := dns.NewRR("acceptance.invalid. 60 IN SOA ns.acceptance.invalid. hostmaster.acceptance.invalid. 1 60 60 60 60")
			r.Ns = []dns.RR{soa}
		} else if q.Question[0].Name == "nodata.acceptance.invalid." || (q.Question[0].Name == "ipv6only.acceptance.invalid." && q.Question[0].Qtype == dns.TypeA) {
			soa, _ := dns.NewRR("acceptance.invalid. 60 IN SOA ns.acceptance.invalid. hostmaster.acceptance.invalid. 1 60 60 60 60")
			r.Ns = []dns.RR{soa}
		} else if q.Question[0].Name == "large.acceptance.invalid." {
			for n := 1; n <= 50; n++ {
				a, _ := dns.NewRR(fmt.Sprintf("large.acceptance.invalid. 60 IN A 192.0.2.%d", n))
				r.Answer = append(r.Answer, a)
			}
		} else if q.Question[0].Name == "cname.acceptance.invalid." && q.Question[0].Qtype == dns.TypeA {
			c, _ := dns.NewRR("cname.acceptance.invalid. 60 IN CNAME target.acceptance.invalid.")
			a, _ := dns.NewRR("target.acceptance.invalid. 60 IN A 192.0.2.9")
			r.Answer = []dns.RR{c, a}
		} else if q.Question[0].Name == "multi.acceptance.invalid." && q.Question[0].Qtype == dns.TypeA {
			for _, ip := range []string{"192.0.2.9", "192.0.2.10"} {
				a, _ := dns.NewRR("multi.acceptance.invalid. 60 IN A " + ip)
				r.Answer = append(r.Answer, a)
			}
		} else if q.Question[0].Qtype == dns.TypeAAAA {
			a, _ := dns.NewRR(q.Question[0].Name + " 60 IN AAAA 2001:db8::9")
			r.Answer = []dns.RR{a}
		} else if q.Question[0].Qtype == dns.TypeA {
			a, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 192.0.2.9")
			r.Answer = []dns.RR{a}
		}
		_ = w.WriteMsg(r)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	defer srv.Shutdown()
	tcpUpstream, e := net.Listen("tcp", upstream.LocalAddr().String())
	if e != nil {
		t.Fatal(e)
	}
	tcpSrv := &dns.Server{Listener: tcpUpstream, Handler: srv.Handler}
	go func() { _ = tcpSrv.ActivateAndServe() }()
	defer tcpSrv.Shutdown()
	global := `global {
 disable_waiting_network: true
 bpf_conn_state_map_size: 2048
 }`
	dnsText := fmt.Sprintf(`dns {
 bind: 'tcp+udp://127.0.0.1:15353'
 ipversion_prefer: 0
 upstream { fixture: 'udp://%s' }
 routing {
 request { fallback: fixture }
 response { fallback: accept }
 }
 }`, upstream.LocalAddr())
	routing := `routing { fallback: direct }`
	conf, err := ParseConfig(&global, &dnsText, &routing)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan *ReloadMessage)
	done := make(chan error, 1)
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	go func() { done <- runCoordinator(log, EmptyConfig, nil, requests) }()
	defer func() {
		close(requests)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(30 * time.Second):
			t.Error("coordinator did not exit")
		}
	}()
	reload := func(msg *ReloadMessage, wantError bool) {
		t.Helper()
		// Keep receive side separately because the protocol exposes a send-only channel.
		reply := make(chan error, 1)
		msg.Callback = reply
		select {
		case requests <- msg:
		case err := <-done:
			t.Fatalf("unexpected exit: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("request stalled")
		}
		select {
		case err := <-reply:
			if (err != nil) != wantError {
				t.Fatalf("reload error=%v expected error=%v", err, wantError)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("reply stalled")
		}
	}
	probe := func(network, name string, qtype uint16) error {
		q := new(dns.Msg)
		q.SetQuestion(name, qtype)
		r, _, err := (&dns.Client{Net: network, Timeout: 2 * time.Second}).Exchange(q, "127.0.0.1:15353")
		if err != nil {
			return err
		}
		if name == "missing.acceptance.invalid." {
			if r.Rcode != dns.RcodeNameError || len(r.Answer) != 0 || len(r.Ns) == 0 {
				return fmt.Errorf("NXDOMAIN lost: %v", r)
			}
		} else if name == "nodata.acceptance.invalid." {
			if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 || len(r.Ns) == 0 {
				return fmt.Errorf("NODATA lost: %v", r)
			}
		} else if qtype == dns.TypeA && (name == "cname.acceptance.invalid." || name == "multi.acceptance.invalid.") {
			if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 2 {
				return fmt.Errorf("lost CNAME/multiple A: %v", r)
			}
			if name == "cname.acceptance.invalid." && r.Answer[0].Header().Rrtype != dns.TypeCNAME {
				return fmt.Errorf("lost alias: %v", r)
			}
		} else if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].Header().Rrtype != qtype {
			return fmt.Errorf("wrong positive answer: %v", r)
		}
		for _, rr := range r.Answer {
			if rr.Header().Ttl > 60 {
				return fmt.Errorf("TTL increased: %v", r)
			}
		}
		return nil
	}
	check := func() {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan error, 80)
		// Distinct transport/type/name and repeated concurrent cache accesses.
		for repeat := 0; repeat < 4; repeat++ {
			for _, network := range []string{"udp", "tcp"} {
				for _, name := range []string{"ok.acceptance.invalid.", "missing.acceptance.invalid.", "nodata.acceptance.invalid.", "cname.acceptance.invalid.", "multi.acceptance.invalid."} {
					for _, qt := range []uint16{dns.TypeA, dns.TypeAAAA} {
						wg.Add(1)
						go func(network, name string, qt uint16) {
							defer wg.Done()
							if e := probe(network, name, qt); e != nil {
								errs <- e
							}
						}(network, name, qt)
					}
				}
			}
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
		if t.Failed() {
			t.FailNow()
		}
	}
	var baseFD, baseG int
	cycles := 5
	if raw := os.Getenv("DAED_ACCEPTANCE_CYCLES"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 100 {
			t.Fatal("invalid acceptance cycle budget")
		}
		cycles = n
	}
	for i := 0; i < cycles; i++ {
		reload(&ReloadMessage{Config: conf}, false)
		check()
		if i == 0 {
			preferText := strings.Replace(dnsText, "ipversion_prefer: 0", "ipversion_prefer: 4", 1)
			preferConf, e := ParseConfig(&global, &preferText, &routing)
			if e != nil {
				t.Fatal(e)
			}
			reload(&ReloadMessage{Config: preferConf}, false)
			for repeat := 0; repeat < 3; repeat++ {
				for _, transport := range []string{"udp", "tcp"} {
					for _, name := range []string{"missing.acceptance.invalid.", "nodata.acceptance.invalid."} {
						for _, qt := range []uint16{dns.TypeA, dns.TypeAAAA} {
							if e := probe(transport, name, qt); e != nil {
								t.Fatal(e)
							}
						}
					}
					if e := probe(transport, "ok.acceptance.invalid.", dns.TypeA); e != nil {
						t.Fatal(e)
					}
					if e := probe(transport, "ipv6only.acceptance.invalid.", dns.TypeAAAA); e != nil {
						t.Fatal(e)
					}
					q := new(dns.Msg)
					q.SetQuestion("ok.acceptance.invalid.", dns.TypeAAAA)
					r, _, e := (&dns.Client{Net: transport, Timeout: 2 * time.Second}).Exchange(q, "127.0.0.1:15353")
					if e != nil || r == nil || r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
						t.Fatalf("IPv4 preference filter: response=%v error=%v", r, e)
					}
				}
			}
			t.Log("IPv4 preference preserves NXDOMAIN/NODATA SOA, filters dual-stack AAAA and retains IPv6-only AAAA on UDP/TCP, repeated cache queries")
			reload(&ReloadMessage{Config: conf}, false)
			tcpConf := *conf
			tcpConf.Dns = conf.Dns
			// Parse separately so nested upstream maps are not shared/mutated.
			tcpText := strings.Replace(dnsText, "fixture: 'udp://", "fixture: 'tcp://", 1)
			parsed, e := ParseConfig(&global, &tcpText, &routing)
			if e != nil {
				t.Fatal(e)
			}
			tcpConf.Dns = parsed.Dns
			reload(&ReloadMessage{Config: &tcpConf}, false)
			check()
			for _, transport := range []string{"udp", "tcp"} {
				q := new(dns.Msg)
				q.SetQuestion("large.acceptance.invalid.", dns.TypeA)
				r, _, e := (&dns.Client{Net: transport, Timeout: 2 * time.Second}).Exchange(q, "127.0.0.1:15353")
				if e != nil {
					t.Fatal(e)
				}
				if transport == "udp" {
					r.Compress = true // Unpack does not preserve the wire compression flag.
					if !r.Truncated || r.Len() > 512 {
						t.Fatalf("UDP oversized answer not truncated: %v", r)
					}
				} else if r.Truncated || len(r.Answer) != 50 {
					t.Fatalf("TCP full answer missing: %v", r)
				}
			}
			reload(&ReloadMessage{Config: conf}, false)
			// Outstanding management work must prevent teardown, and a failed
			// drain must restore the same generation until its lease releases.
			_, release, e := managementSlot.acquire()
			if e != nil {
				t.Fatal(e)
			}
			reload(&ReloadMessage{Config: conf}, true)
			check()
			release()
		}
		reload(&ReloadMessage{Config: conf}, false)
		check()
		// Candidate DNS bind fails after old runtime has stopped; old DNS must
		// return on both transports after the failed acknowledgement.
		occupied, err := net.Listen("tcp", "127.0.0.1:15354")
		if err != nil {
			t.Fatal(err)
		}
		bad := *conf
		bad.Dns = conf.Dns
		bad.Dns.Bind = "tcp+udp://127.0.0.1:15354"
		reload(&ReloadMessage{Config: &bad}, true)
		check()
		_ = occupied.Close()
		reload(&ReloadMessage{Config: EmptyConfig}, false)
		if _, release, err := managementSlot.acquire(); err == nil {
			release()
			t.Fatal("OFF still published")
		}
		for _, network := range []string{"udp", "tcp"} {
			if network == "tcp" {
				l, e := net.Listen(network, "127.0.0.1:15353")
				if e != nil {
					t.Fatal("OFF left TCP DNS", e)
				}
				_ = l.Close()
			} else {
				l, e := net.ListenPacket(network, "127.0.0.1:15353")
				if e != nil {
					t.Fatal("OFF left UDP DNS", e)
				}
				_ = l.Close()
			}
		}
		runtime.GC()
		fds, e := os.ReadDir("/proc/self/fd")
		if e != nil {
			t.Fatal(e)
		}
		g := runtime.NumGoroutine()
		if i == 0 {
			baseFD = len(fds)
			baseG = g
		} else if len(fds) > baseFD+8 || g > baseG+12 {
			t.Fatalf("OFF resource growth fd=%d/%d goroutines=%d/%d", len(fds), baseFD, g, baseG)
		}
	}
	// Closed coordinator has no published management plane, not just an API ACK.
	t.Logf("%d ON/reload/rejected-bind rollback/OFF cycles; concurrent DNS A/AAAA/NXDOMAIN/NODATA; final OFF FD/goroutine bounds passed", cycles)
}
