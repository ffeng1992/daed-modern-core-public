// SPDX-License-Identifier: AGPL-3.0-only
package dae

import (
	"errors"
	"fmt"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/control"
)

// DNS-only transition, owned by one serialized lifecycle coordinator. A bounded
// stop/rebind is deliberately used: the unmodified core's listener ownership
// fields must not be moved while DNS handler goroutines can still read them.
// This creates a brief DNS reload gap; it is not a zero-downtime handoff.
// The caller must separately manage BPF, routing epochs and proxy listeners.
type dnsTransition struct {
	old, candidate         *control.ControlPlane
	oldStopped, rolledBack bool
}

func prepareDNSTransition(old, candidate *control.ControlPlane, oldConf, newConf *daeConfig.Config) (*dnsTransition, error) {
	if old == nil || candidate == nil || old == candidate || oldConf == nil || newConf == nil {
		return nil, fmt.Errorf("DNS handoff requires distinct generations and their configurations")
	}
	h := &dnsTransition{old: old, candidate: candidate}
	candidate.SetPreparedDNSStartHook(func() error {
		h.oldStopped = true
		return old.StopDNSListener()
	})
	return h, nil
}

func (h *dnsTransition) rollback() error {
	if h.rolledBack {
		return nil
	}
	h.candidate.SetPreparedDNSStartHook(nil)
	stopErr := h.candidate.StopDNSListener()
	var restartErr error
	if h.oldStopped {
		restartErr = h.old.RestartDNSListener()
	}
	if err := errors.Join(stopErr, restartErr); err != nil {
		return fmt.Errorf("rollback DNS endpoints: %w", err)
	}
	h.rolledBack = true
	return nil
}
