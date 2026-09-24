//go:build linux && deployment_candidate && !isolated_acceptance

package dae

import "os"

// Explicit operator opt-in; ordinary development builds stay closed.
// This only enables the previously accepted coordinator, not a different path.
func init() {
	liveDatapathValidated = os.Getenv("DAED_ENABLE_VALIDATED_BRIDGE") == "1"
}
