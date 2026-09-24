package dae

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/daeuniverse/dae/control"
)

func TestGenerationPublicationPreservesOutstandingUsers(t *testing.T) {
	var slot generationSlot
	oldPlane, nextPlane := new(control.ControlPlane), new(control.ControlPlane)
	old, err := slot.publish(nil, oldPlane)
	if err != nil {
		t.Fatal(err)
	}
	plane, release, err := slot.acquire()
	if err != nil || plane != oldPlane {
		t.Fatal("wrong initial generation", err)
	}
	next, err := slot.publish(old, nextPlane)
	if err != nil {
		t.Fatal(err)
	}
	plane, releaseNext, err := slot.acquire()
	if err != nil || plane != nextPlane {
		t.Fatal("new requests did not switch", err)
	}
	defer releaseNext()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := old.waitManagementDrain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("retired too early", err)
	}
	// Multiple cleanup paths may release the same lease, including concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); release() }()
	}
	wg.Wait()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.waitManagementDrain(ctx); err != nil {
		t.Fatal(err)
	}
	// A stale owner must not retire or overwrite the new active generation.
	if _, err := slot.publish(old, new(control.ControlPlane)); !errors.Is(err, errGenerationChanged) {
		t.Fatal(err)
	}
	if _, err := slot.publish(next, nextPlane); err == nil {
		t.Fatal("accepted duplicate plane")
	}
	plane, done, err := slot.acquire()
	if err != nil || plane != nextPlane {
		t.Fatal("rejected publication mutated active instance")
	}
	done()
}

func TestGenerationOffWaitsForUsers(t *testing.T) {
	var slot generationSlot
	active, err := slot.publish(nil, new(control.ControlPlane))
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := slot.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slot.publish(active, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := slot.acquire(); !errors.Is(err, ErrControlPlaneNotInit) {
		t.Fatal(err)
	}
	select {
	case <-active.drained:
		t.Fatal("OFF lost outstanding user")
	default:
	}
	release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := active.waitManagementDrain(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := slot.publish(nil, new(control.ControlPlane)); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationConcurrentPublicationAndManagement(t *testing.T) {
	var slot generationSlot
	current, err := slot.publish(nil, new(control.ControlPlane))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 500; j++ {
				plane, release, err := slot.acquire()
				if err != nil || plane == nil {
					t.Error("lost active generation", err)
					return
				}
				release()
			}
		}()
	}
	close(start)
	for i := 0; i < 100; i++ {
		old := current
		current, err = slot.publish(old, new(control.ControlPlane))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = old.waitManagementDrain(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if _, err := slot.publish(current, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := current.waitManagementDrain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationRestoreRetainsOutstandingLease(t *testing.T) {
	var slot generationSlot
	active, err := slot.publish(nil, new(control.ControlPlane))
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := slot.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slot.publish(active, nil); err != nil {
		t.Fatal(err)
	}
	if err = slot.restore(active); err != nil {
		t.Fatal(err)
	}
	_, release2, err := slot.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slot.publish(active, nil); err != nil {
		t.Fatal(err)
	}
	release2()
	select {
	case <-active.drained:
		t.Fatal("lost old lease across restore")
	default:
	}
	release()
	if err = active.waitManagementDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Restore racing the last release must use a new drain signal.
	if err = slot.restore(active); err != nil {
		t.Fatal(err)
	}
	_, release3, err := slot.acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slot.publish(active, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.drained:
		t.Fatal("reused completed signal")
	default:
	}
	release3()
}
