//go:build linux && isolated_acceptance

package dae

import "os"

// Only explicit test binaries include this entrypoint. Default daemon builds
// retain the live gate. Require a separate network namespace as well as CI/root.
func init() {
	self, selfErr := os.Readlink("/proc/self/ns/net")
	host, hostErr := os.Readlink("/proc/1/ns/net")
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" || os.Geteuid() != 0 || selfErr != nil || hostErr != nil || self == host {
		panic("isolated acceptance binary requires root CI in a separate network namespace")
	}
	liveDatapathValidated = true
}
