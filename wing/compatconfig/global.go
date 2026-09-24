// SPDX-License-Identifier: AGPL-3.0-only
package compatconfig

import (
	"fmt"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"strings"
)

func DefaultGlobal() (daeConfig.Global, error) {
	sections, err := config_parser.Parse("global {} routing { fallback: direct }")
	if err != nil {
		return daeConfig.Global{}, err
	}
	c, err := daeConfig.New(sections)
	if err != nil {
		return daeConfig.Global{}, err
	}
	return c.Global, nil
}

// MarshalGlobal uses the public upstream serializer. Global contains scalar/list
// fields only; quoted newlines are escaped by that serializer. Check the shape
// before returning, so an upstream format change fails instead of saving junk.
func MarshalGlobal(g daeConfig.Global) (string, error) {
	c := &daeConfig.Config{Global: g}
	c.Routing.Fallback = "direct"
	c.Dns.Routing.Request.Fallback = "asis"
	c.Dns.Routing.Response.Fallback = "accept"
	b, err := c.Marshal(2)
	if err != nil {
		return "", err
	}
	s := string(b)
	if !strings.HasPrefix(s, "global {\n") {
		return "", fmt.Errorf("unexpected upstream global format")
	}
	end := strings.Index(s, "\n}\n")
	if end < 0 {
		return "", fmt.Errorf("missing global section terminator")
	}
	result := s[:end+3]
	// The core interprets presence of so_mark_from_dae as an explicit mark,
	// including zero. Preserve absence rather than accidentally selecting zero.
	if !g.SoMarkFromDaeSet {
		lines := strings.Split(result, "\n")
		kept := lines[:0]
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "so_mark_from_dae:") {
				continue
			}
			kept = append(kept, line)
		}
		result = strings.Join(kept, "\n")
	}
	sections, err := config_parser.Parse(result)
	if err != nil {
		return "", err
	}
	if len(sections) != 1 || sections[0].Name != "global" {
		return "", fmt.Errorf("invalid serialized global section")
	}
	return result, nil
}
