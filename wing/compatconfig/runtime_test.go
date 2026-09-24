package compatconfig

import (
	"reflect"
	"testing"
)

func TestRuntimeIgnoresLegacySwitchesWithoutChangingStoredConfig(t *testing.T) {
	stored, err := DefaultGlobal()
	if err != nil {
		t.Fatal(err)
	}
	stored.EnableLocalTcpFastRedirect = true
	stored.AutoConfigFirewallRule = true
	stored.BootstrapResolver = "192.0.2.53:53"
	stored.AutoSniffPunt = false
	stored.BpfConnStateMapSize = 4096
	runtime := RuntimeGlobal(stored)
	if runtime.EnableLocalTcpFastRedirect || runtime.AutoConfigFirewallRule {
		t.Fatal("obsolete switch reached runtime")
	}
	if !stored.EnableLocalTcpFastRedirect || !stored.AutoConfigFirewallRule {
		t.Fatal("stored settings mutated")
	}
	runtime.EnableLocalTcpFastRedirect = true
	runtime.AutoConfigFirewallRule = true
	if !reflect.DeepEqual(runtime, stored) {
		t.Fatal("unrelated setting changed")
	}
}
