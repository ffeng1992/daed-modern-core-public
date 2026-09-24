package dae

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestServeReadinessDoesNotConsumeExit(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	want := errors.New("synthetic runtime failure")
	s := startServing(func(ready chan<- bool) error { ready <- true; <-stop; return want })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	// Release the server, then all observers must see the same failure.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.waitExit(ctx); !errors.Is(err, want) {
				t.Errorf("lost termination: %v", err)
			}
		}()
	}
	// Signal without closing twice on a failed assertion.
	stop <- struct{}{}
	wg.Wait()
}

func TestServeExitedCannotAcknowledgeReady(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		want := errors.New("synthetic startup failure")
		s := startServing(func(ready chan<- bool) error {
			if buffered {
				ready <- true
			}
			return want
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := s.waitExit(ctx); !errors.Is(err, want) {
			t.Fatal(err)
		}
		if err := s.waitReady(ctx); !errors.Is(err, want) || !errors.Is(err, errServeBeforeReady) {
			t.Fatal(err)
		}
		cancel()
	}
}

func TestServeReadinessCancellationDoesNotStopServer(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	s := startServing(func(ready chan<- bool) error { <-stop; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.waitReady(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-s.done:
		t.Fatal("readiness cancellation took ownership of server shutdown")
	default:
	}
	stop <- struct{}{}
	exitCtx, exitCancel := context.WithTimeout(context.Background(), time.Second)
	defer exitCancel()
	if err := s.waitExit(exitCtx); err != nil {
		t.Fatal(err)
	}
}

func TestServeNegativeReadiness(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	s := startServing(func(ready chan<- bool) error { ready <- false; <-stop; return nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.waitReady(ctx); !errors.Is(err, errServeNotReady) {
		t.Fatal(err)
	}
}
