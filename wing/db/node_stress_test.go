package db

import (
	_ "github.com/daeuniverse/dae/component/outbound"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Parsing must not dial a server or leave per-node health workers alive.
// The documentation-only endpoint is never contacted by this fixture.
func TestSyntheticNodeParseResourceStress(t *testing.T) {
	link := "ss://YWVzLTEyOC1nY206Zml4dHVyZQ==@192.0.2.1:443#fixture"
	if _, err := NewNodeModel(link, nil, nil); err != nil {
		t.Fatal(err)
	}
	beforeG := runtime.NumGoroutine()
	beforeFD, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				n, err := NewNodeModel(link, nil, nil)
				if err != nil {
					t.Errorf("parse failed: %v", err)
					return
				}
				if n.Address != "192.0.2.1:443" {
					t.Errorf("metadata address lost: %q", n.Address)
					return
				}
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > beforeG+4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	afterFD, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterFD) > len(beforeFD)+2 {
		t.Errorf("FD growth: %d -> %d", len(beforeFD), len(afterFD))
	}
	if g := runtime.NumGoroutine(); g > beforeG+4 {
		t.Errorf("goroutine growth: %d -> %d", beforeG, g)
	}
	t.Logf("8000 parses / 16 workers; FD %d -> %d; goroutines %d -> %d", len(beforeFD), len(afterFD), beforeG, runtime.NumGoroutine())
}
