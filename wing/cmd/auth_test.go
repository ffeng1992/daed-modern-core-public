package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daeuniverse/dae-wing/db"
	appgraphql "github.com/daeuniverse/dae-wing/graphql"
	"github.com/graph-gophers/graphql-go/relay"
)

// Synthetic database only. Exercise the HTTP auth wrapper, not an injected
// administrative context; never include tokens in assertion output.
func TestHTTPManagementAuthentication(t *testing.T) {
	if err := db.InitDatabase(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB(context.Background()).DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	schema, err := appgraphql.Schema()
	if err != nil {
		t.Fatal(err)
	}
	handler := auth(&relay.Handler{Schema: schema})
	request := func(query, token string) map[string]json.RawMessage {
		b, _ := json.Marshal(map[string]string{"query": query})
		r := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("unexpected HTTP status %d", w.Code)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal("invalid response JSON")
		}
		return out
	}
	denied := request(`{ configs { id } }`, "")
	if len(denied["errors"]) == 0 {
		t.Fatal("unauthenticated access accepted")
	}
	created := request(`mutation {createUser(username:"fixture",password:"Fixture12345")}`, "")
	if len(created["errors"]) > 0 {
		t.Fatal("synthetic registration failed")
	}
	var data struct{ CreateUser string }
	if err := json.Unmarshal(created["data"], &data); err != nil {
		t.Fatal(err)
	}
	if data.CreateUser == "" {
		t.Fatal("missing registration token")
	}
	if r := request(`{ configs { id } }`, data.CreateUser); len(r["errors"]) > 0 {
		t.Fatal("valid token denied")
	}
	if r := request(`{ configs { id } }`, "invalid.token.signature"); len(r["errors"]) == 0 {
		t.Fatal("invalid token accepted")
	}
	if r := request(`mutation {createUser(username:"second",password:"Fixture12345")}`, ""); len(r["errors"]) == 0 {
		t.Fatal("unauthorized second registration accepted")
	}
	if r := request(`{token(username:"fixture",password:"wrong123")}`, ""); len(r["errors"]) == 0 {
		t.Fatal("wrong password accepted")
	}
	if r := request(`{token(username:"fixture",password:"Fixture12345")}`, ""); len(r["errors"]) > 0 {
		t.Fatal("correct password denied")
	}
}
