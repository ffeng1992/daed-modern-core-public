package dae

import (
	"context"
	"errors"
	"sync"

	"github.com/daeuniverse/dae/control"
)

var errGenerationChanged = errors.New("active generation changed before publication")

// activeGeneration tracks management users, not datapath flows. A lifecycle
// owner must commit kernel/listener state before publishing and must separately
// drain proxy flows before closing a retired plane.
type activeGeneration struct {
	plane   *control.ControlPlane
	users   int
	retired bool
	drained chan struct{}
}

type generationSlot struct {
	mu      sync.Mutex
	current *activeGeneration
}

// publish is called by the serialized lifecycle owner. expected guards against
// stale commits; a failed publication leaves both generations untouched.
func (s *generationSlot) publish(expected *activeGeneration, plane *control.ControlPlane) (*activeGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != expected {
		return nil, errGenerationChanged
	}
	if plane != nil && expected != nil && plane == expected.plane {
		return nil, errors.New("cannot replace generation with the same plane")
	}
	var next *activeGeneration
	if plane != nil {
		next = &activeGeneration{plane: plane, drained: make(chan struct{})}
	}
	s.current = next
	if expected != nil {
		expected.retired = true
		if expected.users == 0 {
			close(expected.drained)
		}
	}
	return next, nil
}

// acquire pins the selected plane until release. For a returned connection,
// the caller must retain this lease through connection Close, not just Dial.
// A management request must not call back into lifecycle publication while
// retaining a lease that the same publication waits to drain.
func (s *generationSlot) acquire() (*control.ControlPlane, func(), error) {
	s.mu.Lock()
	g := s.current
	if g == nil {
		s.mu.Unlock()
		return nil, nil, ErrControlPlaneNotInit
	}
	g.users++
	s.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			g.users--
			if g.retired && g.users == 0 {
				close(g.drained)
			}
		})
	}
	return g.plane, release, nil
}

// waitManagementDrain never force-closes a plane on timeout. The owner must
// retain the retired generation for later cleanup; timeout is not completion.
func (g *activeGeneration) waitManagementDrain(ctx context.Context) error {
	select {
	case <-g.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// restore is only valid after a drain timeout, before any teardown. Outstanding
// users still belong to g, so allocating a replacement counter would be unsafe.
func (s *generationSlot) restore(g *activeGeneration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g == nil || s.current != nil || !g.retired {
		return errGenerationChanged
	}
	select {
	case <-g.drained:
		// The final user may have released just as the deadline expired.
		g.drained = make(chan struct{})
	default:
	}
	g.retired = false
	s.current = g
	return nil
}
