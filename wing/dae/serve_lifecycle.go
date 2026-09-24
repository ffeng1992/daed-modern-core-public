package dae

import (
	"context"
	"errors"
)

var errServeBeforeReady = errors.New("core serve loop exited before readiness")
var errServeNotReady = errors.New("core refused readiness")

// serveLifecycle separates startup acknowledgement from termination. A reload
// owner waits for readiness once; any observers may await the same exit result.
// It does not own the plane or listener: the owner must close them on failure.
type serveLifecycle struct {
	ready chan bool
	done  chan struct{}
	err   error // published by closing done; never read before done is closed
}

func startServing(serve func(chan<- bool) error) *serveLifecycle {
	s := &serveLifecycle{ready: make(chan bool, 1), done: make(chan struct{})}
	go func() {
		s.err = serve(s.ready)
		close(s.done)
	}()
	return s
}

func (s *serveLifecycle) waitReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.Join(errServeBeforeReady, s.err)
	case ok := <-s.ready:
		if err := ctx.Err(); err != nil {
			return err
		}
		// A buffered ready signal must not hide an already exited server.
		select {
		case <-s.done:
			return errors.Join(errServeBeforeReady, s.err)
		default:
		}
		if !ok {
			return errServeNotReady
		}
		return nil
	}
}

func (s *serveLifecycle) waitExit(ctx context.Context) error {
	select {
	case <-s.done:
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
