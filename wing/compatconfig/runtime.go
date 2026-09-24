// SPDX-License-Identifier: AGPL-3.0-only
package compatconfig

import daeConfig "github.com/daeuniverse/dae/config"

// RuntimeGlobal leaves stored/UI values intact while neutralizing switches that
// the pinned core declares unused. Review this boundary on every core upgrade;
// do not silently reuse it if upstream assigns a new meaning to either field.
func RuntimeGlobal(stored daeConfig.Global) daeConfig.Global {
	stored.EnableLocalTcpFastRedirect = false
	stored.AutoConfigFirewallRule = false
	return stored
}
