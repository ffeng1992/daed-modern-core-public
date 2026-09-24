package dae

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daeuniverse/dae/component/sniffing"
)

// A closed handle must never acquire ownership of another connection's state.
// Running this with -race also catches publication before sync.Once returns.
func TestSnifferLifetimeAfterClose(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 200; j++ {
				old := sniffing.NewStreamSniffer(strings.NewReader(""), time.Second)
				_ = old.Close()
				current := sniffing.NewStreamSniffer(strings.NewReader("GET / HTTP/1.1\r\nHost: lifetime.acceptance.invalid\r\n\r\n"), time.Second)
				_ = old.Close()
				domain, err := current.SniffTcp()
				if err != nil || domain != "lifetime.acceptance.invalid" {
					t.Errorf("reused closed handle damaged new connection: domain=%q err=%v", domain, err)
				}
				_ = current.Close()
			}
		}()
	}
	workers.Wait()
}
