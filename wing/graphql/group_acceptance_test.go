package graphql

import (
	"context"
	"encoding/json"
	"github.com/daeuniverse/dae-wing/db"
	"testing"
)

func TestSyntheticNodeGroupPersistence(t *testing.T) {
	dir := t.TempDir()
	if e := db.InitDatabase(dir); e != nil {
		t.Fatal(e)
	}
	defer func() { sql, _ := db.DB(context.Background()).DB(); _ = sql.Close() }()
	schema, e := Schema()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(context.Background(), "role", "ADMIN")
	exec := func(q string, v map[string]interface{}) []byte {
		t.Helper()
		r := schema.Exec(ctx, q, "", v)
		if len(r.Errors) > 0 {
			t.Fatal(r.Errors)
		}
		return r.Data
	}
	data := exec(`mutation{importNodes(rollbackError:true,args:[{link:"ss://YWVzLTEyOC1nY206c3ludGhldGljLWFjY2VwdGFuY2Utb25seQ@192.0.2.1:19090#fixture"}]){error node{id}}}`, nil)
	var imported struct {
		ImportNodes []struct {
			Error *string
			Node  struct{ ID string }
		}
	}
	if e = json.Unmarshal(data, &imported); e != nil {
		t.Fatal(e)
	}
	if len(imported.ImportNodes) != 1 || imported.ImportNodes[0].Error != nil || imported.ImportNodes[0].Node.ID == "" {
		t.Fatalf("import failed: %s", data)
	}
	nodeID := imported.ImportNodes[0].Node.ID
	data = exec(`mutation{createGroup(name:"fixture",policy:fixed,policyParams:[{val:"0"}]){id}}`, nil)
	var created struct{ CreateGroup struct{ ID string } }
	if e = json.Unmarshal(data, &created); e != nil || created.CreateGroup.ID == "" {
		t.Fatalf("create: %v %s", e, data)
	}
	vars := map[string]interface{}{"id": created.CreateGroup.ID, "nodes": []interface{}{nodeID}}
	exec(`mutation($id:ID!,$nodes:[ID!]!){groupAddNodes(id:$id,nodeIDs:$nodes)}`, vars)
	query := `{groups{name policy policyParams{val} nodes{id name protocol link}} nodes{totalCount edges{id link}}}`
	before := string(exec(query, nil))
	sql, e := db.DB(ctx).DB()
	if e != nil {
		t.Fatal(e)
	}
	if e = sql.Close(); e != nil {
		t.Fatal(e)
	}
	if e = db.InitDatabase(dir); e != nil {
		t.Fatal(e)
	}
	schema, e = Schema()
	if e != nil {
		t.Fatal(e)
	}
	if after := string(exec(query, nil)); before != after {
		t.Fatal("node/group association lost on reopen")
	}
	exec(`mutation($id:ID!){groupSetPolicy(id:$id,policy:min)}`, map[string]interface{}{"id": created.CreateGroup.ID})
	exec(`mutation($id:ID!,$nodes:[ID!]!){groupDelNodes(id:$id,nodeIDs:$nodes)}`, vars)
	exec(`mutation($id:ID!){renameGroup(id:$id,name:"renamed") removeGroup(id:$id)}`, map[string]interface{}{"id": created.CreateGroup.ID})
	exec(`mutation($nodes:[ID!]!){removeNodes(ids:$nodes)}`, map[string]interface{}{"nodes": []interface{}{nodeID}})
	var final struct {
		Groups []interface{}
		Nodes  struct{ TotalCount int }
	}
	data = exec(`{groups{id} nodes{totalCount}}`, nil)
	if e = json.Unmarshal(data, &final); e != nil || len(final.Groups) != 0 || final.Nodes.TotalCount != 0 {
		t.Fatalf("cleanup: %v %s", e, data)
	}
	t.Log("synthetic SS import, group policy/association and DB reopen verified; no provider subscription used")
}
