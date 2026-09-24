package dae

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestReloadRequestExitAndCancelledClient(t *testing.T) {
	oldReq, oldExit := ChReloadConfigs, GracefullyExit
	defer func() { ChReloadConfigs = oldReq; GracefullyExit = oldExit }()
	ChReloadConfigs = make(chan *ReloadMessage)
	GracefullyExit = make(chan struct{})
	close(GracefullyExit)
	if err := SubmitReload(context.Background(), EmptyConfig); err == nil {
		t.Fatal("dead runtime accepted reload")
	}
	GracefullyExit = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	accepted := make(chan struct{})
	finish := make(chan struct{})
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		msg := <-ChReloadConfigs
		close(accepted)
		<-finish
		msg.Callback <- nil
	}()
	result := make(chan error, 1)
	go func() { result <- SubmitReload(ctx, EmptyConfig) }()
	<-accepted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(finish)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("abandoned client blocked coordinator")
	}
}

func TestManagementConnectionLeaseLivesUntilClose(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	releases := 0
	conn := &leasedManagementConn{Conn: left, release: func() { releases++ }}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = conn.Close() }()
	}
	wg.Wait()
	if releases != 1 {
		t.Fatalf("release count %d", releases)
	}
}
