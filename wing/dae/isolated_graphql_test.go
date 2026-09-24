//go:build linux && !dae_stub_ebpf

package dae_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql"
	"github.com/miekg/dns"
	"net"
	"os"
	"testing"
	"time"
)

func TestIsolatedGraphQLRuntime(t *testing.T) {
	if os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" {
		t.Skip("isolated CI only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Geteuid() != 0 {
		t.Fatal("disposable root required")
	}
	if e := db.InitDatabase(t.TempDir()); e != nil {
		t.Fatal(e)
	}
	defer func() { sql, _ := db.DB(context.Background()).DB(); _ = sql.Close() }()
	oldReq, oldExit := dae.ChReloadConfigs, dae.GracefullyExit
	dae.ChReloadConfigs, dae.GracefullyExit = make(chan *dae.ReloadMessage), make(chan struct{})
	defer func() { dae.ChReloadConfigs, dae.GracefullyExit = oldReq, oldExit }()
	done := make(chan error, 1)
	go func() { done <- dae.RunIsolatedCoordinatorForTest() }()
	defer func() {
		close(dae.ChReloadConfigs)
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(30 * time.Second):
			t.Error("runtime shutdown stalled")
		}
	}()
	schema, e := graphql.Schema()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), "role", "ADMIN")
	exec := func(q string, v map[string]interface{}, wantErr bool) []byte {
		t.Helper()
		r := schema.Exec(ctx, q, "", v)
		if (len(r.Errors) > 0) != wantErr {
			t.Fatalf("GraphQL error=%v expected=%v", r.Errors, wantErr)
		}
		return r.Data
	}
	create := func(kind, input string) string {
		t.Helper()
		data := exec(fmt.Sprintf(`mutation{create%s(name:"fixture",%s){id}}`, kind, input), nil, false)
		var result map[string]struct{ ID string }
		if e := json.Unmarshal(data, &result); e != nil {
			t.Fatal(e)
		}
		id := result["create"+kind].ID
		if id == "" {
			t.Fatal("missing id")
		}
		exec(fmt.Sprintf(`mutation($id:ID!){select%s(id:$id)}`, kind), map[string]interface{}{"id": id}, false)
		return id
	}
	create("Config", `global:{disableWaitingNetwork:true,bpfConnStateMapSize:2048}`)
	goodID := create("Dns", `dns:"bind: 'tcp+udp://127.0.0.1:15363'\nipversion_prefer: 0\nrouting { request { fallback: reject } response { fallback: accept } }"`)
	create("Routing", `routing:"fallback: direct"`)
	checkState := func(on bool) {
		t.Helper()
		var system db.System
		if e := db.DB(ctx).First(&system).Error; e != nil {
			t.Fatal(e)
		}
		if system.Running != on {
			t.Fatalf("stored running=%v expected=%v", system.Running, on)
		}
	}
	checkDNS := func() {
		t.Helper()
		for _, transport := range []string{"udp", "tcp"} {
			q := new(dns.Msg)
			q.SetQuestion("fixture.invalid.", dns.TypeA)
			r, _, e := (&dns.Client{Net: transport, Timeout: time.Second}).Exchange(q, "127.0.0.1:15363")
			if e != nil || r == nil {
				t.Fatalf("live DNS after API acknowledgement: %v", e)
			}
		}
	}
	exec(`mutation{run(dry:false)}`, nil, false)
	checkState(true)
	checkDNS()
	occupied, e := net.Listen("tcp", "127.0.0.1:15364")
	if e != nil {
		t.Fatal(e)
	}
	defer occupied.Close()
	create("Dns", `dns:"bind: 'tcp+udp://127.0.0.1:15364'\nrouting { request { fallback: reject } response { fallback: accept } }"`)
	exec(`mutation{run(dry:false)}`, nil, true)
	checkState(true)
	checkDNS()
	exec(`mutation($id:ID!){selectDns(id:$id)}`, map[string]interface{}{"id": goodID}, false)
	exec(`mutation{run(dry:false)}`, nil, false)
	checkDNS()
	exec(`mutation{run(dry:true)}`, nil, false)
	checkState(false)
	tcp, e := net.Listen("tcp", "127.0.0.1:15363")
	if e != nil {
		t.Fatal("OFF retained DNS", e)
	}
	_ = tcp.Close()
	udp, e := net.ListenPacket("udp", "127.0.0.1:15363")
	if e != nil {
		t.Fatal("OFF retained UDP DNS", e)
	}
	_ = udp.Close()
	t.Log("GraphQL ON/reload failure with live rollback/OFF agrees with DB and real DNS sockets")
}
