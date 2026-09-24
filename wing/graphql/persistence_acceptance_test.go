package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/daeuniverse/dae-wing/common"
	"github.com/daeuniverse/dae-wing/db"
)

// This exercises persistent management state, never a live proxy or production DB.
func TestManagementPersistenceAndRejectedWrites(t *testing.T) {
	dir := t.TempDir()
	if err := db.InitDatabase(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { sql, _ := db.DB(context.Background()).DB(); _ = sql.Close() }()
	s, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), "role", "ADMIN")
	exec := func(query string, vars map[string]interface{}, reject bool) []byte {
		t.Helper()
		r := s.Exec(ctx, query, "", vars)
		if reject != (len(r.Errors) > 0) {
			t.Fatalf("unexpected result for %s: %v", query, r.Errors)
		}
		return r.Data
	}
	var ids = map[string]string{}
	for _, fixture := range []struct{ kind, input string }{
		{"Config", `global:{dialMode:"domain",tproxyPort:12345,bootstrapResolver:"192.0.2.53:53",autoSniffPunt:false,bpfConnStateMapSize:4096,tlsFragment:true,tlsFragmentLength:"20-40",tlsFragmentInterval:"5-8",enableLocalTcpFastRedirect:true,autoConfigFirewallRule:true}`},
		{"Dns", `dns:"ipversion_prefer: 4"`},
		{"Routing", `routing:"fallback: direct"`},
	} {
		data := exec(fmt.Sprintf(`mutation { create%s(name:"synthetic",%s) { id } }`, fixture.kind, fixture.input), nil, false)
		var parsed map[string]struct{ ID string }
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatal(err)
		}
		id := parsed["create"+fixture.kind].ID
		if id == "" {
			t.Fatal("missing created ID")
		}
		ids[fixture.kind] = id
		exec(fmt.Sprintf(`mutation($id:ID!){select%s(id:$id)}`, fixture.kind), map[string]interface{}{"id": id}, false)
	}
	query := `{ configs { id name selected global { dialMode tproxyPort bootstrapResolver autoSniffPunt bpfConnStateMapSize tlsFragment tlsFragmentLength tlsFragmentInterval enableLocalTcpFastRedirect autoConfigFirewallRule } } dnss { id name selected dns { string } } routings { id name selected routing { string } } }`
	before := string(exec(query, nil, false))
	// An old-page save omits every new-core field. Those fields and retained
	// legacy switch values must survive both serialization and DB reopen.
	exec(`mutation($id:ID!){updateConfig(id:$id,global:{dialMode:"domain",tproxyPort:12345}){id}}`, map[string]interface{}{"id": ids["Config"]}, false)
	if got := string(exec(query, nil, false)); got != before {
		t.Fatal("old-page save erased new-core settings")
	}

	// Valid ID encoding, nonexistent row: selection must not clear the old selection.
	for _, kind := range []string{"Config", "Dns", "Routing"} {
		exec(fmt.Sprintf(`mutation($id:ID!){select%s(id:$id)}`, kind), map[string]interface{}{"id": string(common.EncodeCursor(999999))}, true)
		if got := string(exec(query, nil, false)); got != before {
			t.Fatalf("rejected %s selection changed state", kind)
		}
	}
	for _, fixture := range []struct{ kind, input string }{
		{"Config", `global:{tproxyPort:-1,dialMode:"ip"}`},
		{"Config", `global:{tproxyPort:65536}`},
		{"Dns", `dns:"upstream {"`},
		{"Routing", `routing:"domain("`},
	} {
		exec(fmt.Sprintf(`mutation($id:ID!){update%s(id:$id,%s){id}}`, fixture.kind, fixture.input), map[string]interface{}{"id": ids[fixture.kind]}, true)
		if got := string(exec(query, nil, false)); got != before {
			t.Fatalf("rejected %s update changed state", fixture.kind)
		}
	}
	// Reopen the same SQLite file and recreate the executable schema. This is DB
	// persistence acceptance, not a claim of a full daemon cold-start test.
	sql, err := db.DB(context.Background()).DB()
	if err != nil {
		t.Fatal(err)
	}
	if err = sql.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.InitDatabase(dir); err != nil {
		t.Fatal(err)
	}
	s, err = Schema()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(exec(query, nil, false)); got != before {
		t.Fatal("reopened database changed stored configuration")
	}
	for _, kind := range []string{"Config", "Dns", "Routing"} {
		vars := map[string]interface{}{"id": ids[kind]}
		exec(fmt.Sprintf(`mutation($id:ID!){rename%s(id:$id,name:"renamed")}`, kind), vars, false)
		exec(fmt.Sprintf(`mutation($id:ID!){remove%s(id:$id)}`, kind), vars, false)
	}
	var remaining map[string][]interface{}
	if err := json.Unmarshal(exec(`{configs{id} dnss{id} routings{id}}`, nil, false), &remaining); err != nil {
		t.Fatal(err)
	}
	for kind, rows := range remaining {
		if len(rows) != 0 {
			t.Fatalf("%s rows remain after delete", kind)
		}
	}
	t.Log("config/DNS/routing create-select-reject-reopen-rename-delete verified with synthetic SQLite state")
}
