// SPDX-License-Identifier: AGPL-3.0-only
package compatconfig

import (
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"reflect"
	"testing"
	"time"
)

func TestGlobalRoundTrip(t *testing.T) {
	g, err := DefaultGlobal()
	if err != nil {
		t.Fatal(err)
	}
	g.AutoSniffPunt = false
	g.TproxyPortProtect = false
	g.SoMarkFromDae = 0
	g.SniffingTimeout = 17 * time.Millisecond
	g.LanInterface = []string{"testlan0", "testlan1"}
	text, err := MarshalGlobal(g)
	if err != nil {
		t.Fatal(err)
	}
	sections, err := config_parser.Parse(text + "\nrouting { fallback: direct }")
	if err != nil {
		t.Fatal(err)
	}
	c, err := daeConfig.New(sections)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, c.Global) {
		t.Fatalf("global changed on round trip: before=%+v after=%+v", g, c.Global)
	}
}
func TestGlobalQuotedTerminatorCannotEscape(t *testing.T) {
	g, err := DefaultGlobal()
	if err != nil {
		t.Fatal(err)
	}
	g.TcpCheckUrl = []string{"https://example.invalid/quoted\"\n}\nrouting { fallback: block }"}
	s, err := MarshalGlobal(g)
	if err != nil {
		t.Fatal(err)
	}
	sections, err := config_parser.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 {
		t.Fatalf("injected sections: %d", len(sections))
	}
}

func TestExplicitZeroMarkSurvives(t *testing.T) {
	g, err := DefaultGlobal()
	if err != nil {
		t.Fatal(err)
	}
	g.SoMarkFromDae = 0
	g.SoMarkFromDaeSet = true
	s, err := MarshalGlobal(g)
	if err != nil {
		t.Fatal(err)
	}
	sections, err := config_parser.Parse(s + "\nrouting { fallback: direct }")
	if err != nil {
		t.Fatal(err)
	}
	c, err := daeConfig.New(sections)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Global.SoMarkFromDaeSet || c.Global.SoMarkFromDae != 0 {
		t.Fatal("explicit zero mark lost")
	}
}
