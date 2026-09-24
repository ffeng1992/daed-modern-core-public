package graphql

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/daeuniverse/dae-wing/db"
)

// Validate the actual UI documents against the new executable backend schema.
func TestFrontendDocuments(t *testing.T) {
	s, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("../../apps/web/src/apis/*.ts")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("(?s)graphql\\(\\s*`(.*?)`")
	count := 0
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range re.FindAllStringSubmatch(string(b), -1) {
			count++
			variables := map[string]interface{}{}
			declarations := regexp.MustCompile(`\$([A-Za-z_][A-Za-z_0-9]*):\s*([A-Za-z_\[\]!0-9]+)`)
			for _, declaration := range declarations.FindAllStringSubmatch(match[1], -1) {
				typ := strings.TrimSuffix(declaration[2], "!")
				var value interface{}
				if strings.HasPrefix(typ, "[") {
					value = []interface{}{}
				} else {
					switch typ {
					case "ID", "String":
						value = "synthetic"
					case "Boolean":
						value = false
					case "Int":
						value = int32(1)
					case "Policy":
						value = "fixed"
					case "globalInput":
						value = map[string]interface{}{}
					case "ImportArgument":
						value = map[string]interface{}{"link": "https://example.invalid/fixture"}
					default:
						t.Fatalf("missing fixture for variable type %s", typ)
					}
				}
				variables[declaration[1]] = value
			}
			if errs := s.ValidateWithVariables(match[1], variables); len(errs) > 0 {
				t.Errorf("%s: %v", filepath.Base(file), errs)
			}
		}
	}
	if count == 0 {
		t.Fatal("no frontend operations found")
	}
	if errs := s.Validate(`{ configs { definitelyMissingField } }`); len(errs) == 0 {
		t.Fatal("schema negative control was accepted")
	}
	t.Logf("validated %d frontend operations with synthetic variable values", count)
}

func TestSyntheticManagementAndConcurrentReads(t *testing.T) {
	if err := db.InitDatabase(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB(context.Background()).DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	s, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), "role", "ADMIN")
	if r := s.Exec(context.Background(), `{ configs { id } }`, "", nil); len(r.Errors) == 0 {
		t.Fatal("unauthorized config access accepted")
	}
	r := s.Exec(ctx, `mutation { createConfig(name:"synthetic",global:{dialMode:"domain",allowInsecure:false}) { id global { dialMode allowInsecure tproxyPort } } }`, "", nil)
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	var result struct {
		CreateConfig struct {
			ID     string
			Global struct {
				DialMode      string
				AllowInsecure bool
				TproxyPort    int
			}
		}
	}
	if err := json.Unmarshal(r.Data, &result); err != nil {
		t.Fatal(err)
	}
	if result.CreateConfig.Global.DialMode != "domain" || result.CreateConfig.Global.AllowInsecure || result.CreateConfig.Global.TproxyPort != 12345 {
		t.Fatalf("defaults corrupted: %s", r.Data)
	}
	r = s.Exec(ctx, `mutation($id:ID!){ updateConfig(id:$id,global:{checkInterval:"45s"}) { global { dialMode checkInterval allowInsecure } } }`, "", map[string]interface{}{"id": result.CreateConfig.ID})
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	if !strings.Contains(string(r.Data), `"domain"`) || !strings.Contains(string(r.Data), `"45s"`) {
		t.Fatalf("partial update corrupted: %s", r.Data)
	}
	// 32 clients x 100 reads exercise the actual GraphQL resolvers and SQLite.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r := s.Exec(ctx, `{ configs { name global { dialMode checkInterval allowInsecure } } }`, "", nil)
				if len(r.Errors) > 0 || !strings.Contains(string(r.Data), `"45s"`) {
					t.Errorf("concurrent read failed: %v", r.Errors)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Log("3200 concurrent management reads completed (not datapath traffic)")
}
