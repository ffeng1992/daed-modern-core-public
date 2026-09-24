//go:build linux && !dae_stub_ebpf

package dae

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/daeuniverse/dae/control"
	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
)

// Explicitly enabled only on a disposable Linux CI machine inside unshare -n.
// Never run this against a production namespace: real BPF is loaded here.
func TestIsolatedPreparedDNSOwnership(t *testing.T) {
	if os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" {
		t.Skip("requires explicit disposable-kernel acceptance")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("requires disposable root CI runner")
	}
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	startUpstream := func(address string) string {
		upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &dns.Server{PacketConn: upstream, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			m := new(dns.Msg)
			m.SetReply(r)
			if len(r.Question) > 0 {
				q := r.Question[0]
				if q.Name == "missing.acceptance.invalid." {
					m.Rcode = dns.RcodeNameError
					rr, _ := dns.NewRR("acceptance.invalid. 60 IN SOA ns.acceptance.invalid. hostmaster.acceptance.invalid. 1 60 60 60 60")
					m.Ns = []dns.RR{rr}
				} else if q.Qtype == dns.TypeA {
					rr, _ := dns.NewRR(q.Name + " 60 IN A " + address)
					m.Answer = []dns.RR{rr}
				}
			}
			_ = w.WriteMsg(m)
		})}
		go func() { _ = server.ActivateAndServe() }()
		t.Cleanup(func() { _ = server.Shutdown() })
		return upstream.LocalAddr().String()

	}
	upstream := startUpstream("192.0.2.9")
	nextUpstream := startUpstream("192.0.2.10")
	bind := "127.0.0.1:15353"
	glob := `global {
 disable_waiting_network: true
 bpf_conn_state_map_size: 2048
 }`
	dnsText := fmt.Sprintf(`dns {
 bind: 'tcp+udp://%s'
 ipversion_prefer: 0
 upstream {
 fixture: 'udp://%s'
 }
 routing {
 request { fallback: fixture }
 response { fallback: accept }
 }
 }`, bind, upstream)
	routing := `routing { fallback: direct }`
	conf, err := ParseConfig(&glob, &dnsText, &routing)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active, err := buildControlPlane(ctx, log, nil, nil, conf, nil, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var listener *control.Listener
	err = control.GetDaeNetns().WithRequired("acceptance listener", func() error {
		var err error
		listener, err = active.Listen(conf.Global.TproxyPort)
		return err
	})
	if err != nil {
		_ = active.Close()
		t.Fatal(err)
	}
	activeLife := startServing(func(ready chan<- bool) error { return active.Serve(ready, listener) })
	defer func() {
		_ = listener.Close()
		// Match the official daemon's orderly shutdown: detach hooks and
		// remove the namespace before closing the owning control plane.
		if err := active.DetachBpfHooks(); err != nil {
			t.Error(err)
		}
		if err := control.GetDaeNetns().Close(); err != nil {
			t.Error(err)
		}
		if err := active.AbortConnections(); err != nil {
			t.Error(err)
		}
		if err := active.Close(); err != nil {
			t.Error(err)
		}
		exitCtx, exitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer exitCancel()
		if err := activeLife.waitExit(exitCtx); err != nil {
			t.Errorf("active serve exit: %v", err)
		}
		control.ResetGlobalUdpState()
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	readyErr := activeLife.waitReady(readyCtx)
	readyCancel()
	if readyErr != nil {
		t.Fatal(readyErr)
	}
	check := func(endpoint string, answers ...string) {
		t.Helper()
		expected := "192.0.2.9"
		if len(answers) > 0 {
			expected = answers[0]
		}
		for _, network := range []string{"udp", "tcp"} {
			for _, name := range []string{"ok.acceptance.invalid.", "missing.acceptance.invalid."} {
				q := new(dns.Msg)
				q.SetQuestion(name, dns.TypeA)
				r, _, err := (&dns.Client{Net: network, Timeout: 3 * time.Second}).Exchange(q, endpoint)
				if err != nil {
					t.Fatalf("%s %s: %v", network, name, err)
				}
				if name == "missing.acceptance.invalid." {
					if r.Rcode != dns.RcodeNameError || len(r.Ns) == 0 {
						t.Fatalf("bad negative answer: %s", r)
					}
				} else if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != expected {
					t.Fatalf("bad A answer: %s", r)
				}
			}
		}
	}
	check(bind)
	if os.Getenv("DAED_ACCEPTANCE_CASE") == "single-generation" {
		t.Log("single-generation control: no candidate, DNS handoff or rollback executed")
		return
	}
	resources := func() (int, int) {
		fds, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(fds), runtime.NumGoroutine()
	}
	baseFD, baseG := resources()
	for i := 0; i < 10; i++ {
		candidate, err := buildControlPlane(ctx, log, active.PeekBpf(), nil, conf, nil, true, true, true)
		if err != nil {
			t.Fatalf("prepare %d: %v", i, err)
		}
		defer candidate.Close()
		// The candidate must leave the active UDP/TCP listener usable.
		check(bind)
		if err := candidate.Close(); err != nil {
			t.Fatalf("discard candidate: %v", err)
		}
		check(bind)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		fd, g := resources()
		if fd <= baseFD+4 && g <= baseG+4 {
			t.Logf("discard resources: FD %d -> %d, goroutines %d -> %d", baseFD, fd, baseG, g)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate resource growth: FD %d -> %d, goroutines %d -> %d", baseFD, fd, baseG, g)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, differentBind := range []bool{false, true} {
		nextDNS := strings.Replace(dnsText, upstream, nextUpstream, 1)
		nextConf, err := ParseConfig(&glob, &nextDNS, &routing)
		if err != nil {
			t.Fatal(err)
		}
		next := *nextConf
		if differentBind {
			next.Dns.Bind = "tcp+udp://127.0.0.1:15354"
		}
		candidate, err := buildControlPlane(ctx, log, active.PeekBpf(), nil, &next, nil, true, false, true)
		if err != nil {
			t.Fatal(err)
		}
		defer candidate.Close()
		handoff, err := prepareDNSTransition(active, candidate, conf, &next)
		if err != nil {
			_ = candidate.Close()
			t.Fatal(err)
		}
		if err := active.LinkRoutingEpochPeer(candidate); err != nil {
			t.Fatal(err)
		}
		cloned, err := listener.Clone()
		if err != nil {
			t.Fatal(err)
		}
		defer cloned.Close()
		candidateLife := startServing(func(ready chan<- bool) error { return candidate.Serve(ready, cloned) })
		nextCtx, nextCancel := context.WithTimeout(ctx, 5*time.Second)
		err = candidateLife.waitReady(nextCtx)
		nextCancel()
		if err != nil {
			t.Fatal(err)
		}

		check(strings.TrimPrefix(next.Dns.Bind, "tcp+udp://"), "192.0.2.10")
		if err := handoff.rollback(); err != nil {
			t.Fatal(err)
		}
		if err := handoff.rollback(); err != nil {
			t.Fatal("rollback must be idempotent", err)
		}
		_ = cloned.Close()
		if err := candidate.RollbackPreparedRoutingEpoch(); err != nil {
			t.Fatal(err)
		}
		if err := candidate.Close(); err != nil {
			t.Fatal(err)
		}
		exitCtx, exitCancel := context.WithTimeout(ctx, 5*time.Second)
		err = candidateLife.waitExit(exitCtx)
		exitCancel()
		if err != nil {
			t.Fatalf("candidate serve exit: %v", err)
		}
		if err := active.PublishListenerSockets(listener); err != nil {
			t.Fatal(err)
		}
		if err := active.RebuildReloadDatapath(); err != nil {
			t.Fatal(err)
		}
		check(bind)
	}

	// Failure after stopping the previous endpoint must recover it, and a
	// failed TCP bind must not leave a half-started candidate UDP listener.
	occupied, err := net.Listen("tcp", "127.0.0.1:15354")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	conflicting := *conf
	conflicting.Dns.Bind = "tcp+udp://127.0.0.1:15354"
	candidate, err := buildControlPlane(ctx, log, active.PeekBpf(), nil, &conflicting, nil, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	handoff, err := prepareDNSTransition(active, candidate, conf, &conflicting)
	if err != nil {
		_ = candidate.Close()
		t.Fatal(err)
	}
	if err := candidate.StartPreparedDNSListener(); err == nil {
		_ = handoff.rollback()
		_ = candidate.Close()
		t.Fatal("conflicting TCP bind unexpectedly succeeded")
	}
	if err := handoff.rollback(); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	check(bind)
	freed, err := net.ListenPacket("udp", "127.0.0.1:15354")
	if err != nil {
		t.Fatalf("candidate left a UDP listener after failed TCP bind: %v", err)
	}
	_ = freed.Close()
	t.Log("forced TCP bind conflict returned error, restored old UDP/TCP DNS and released candidate UDP port")
	t.Log("same-endpoint DNS stop/rebind and different-endpoint rebind both restored old DNS responses after rollback")

	t.Log("10 prepared candidates discarded; active UDP/TCP A and NXDOMAIN+SOA remained usable; no handoff/forwarding claim")
}
