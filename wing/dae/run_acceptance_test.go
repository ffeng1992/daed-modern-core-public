package dae

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestAPIOnlyNeverAcknowledgesDatapathSuccess(t *testing.T) {
	oldReload, oldExit := ChReloadConfigs, GracefullyExit
	ChReloadConfigs, GracefullyExit = make(chan *ReloadMessage), make(chan struct{})
	defer func() { ChReloadConfigs, GracefullyExit = oldReload, oldExit }()
	done := make(chan error, 1)
	go func() { done <- Run(logrus.New(), EmptyConfig, nil, true, true) }()
	defer func() {
		select {
		case ChReloadConfigs <- nil:
		case <-time.After(5 * time.Second):
			t.Fatal("API loop did not stop")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("API loop did not exit")
		}
	}()
	for i := 0; i < 100; i++ {
		callback := make(chan error, 1)
		select {
		case ChReloadConfigs <- &ReloadMessage{Config: EmptyConfig, Callback: callback}:
		case <-time.After(5 * time.Second):
			t.Fatal("request blocked")
		}
		select {
		case err := <-callback:
			if !errors.Is(err, ErrAPIOnly) {
				t.Fatalf("false datapath acknowledgement: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("callback blocked")
		}
	}
}

func TestUnvalidatedLiveDatapathFailsClosed(t *testing.T) {
	oldExit := GracefullyExit
	GracefullyExit = make(chan struct{})
	defer func() { GracefullyExit = oldExit }()
	err := Run(logrus.New(), EmptyConfig, nil, true, false)
	if err == nil || !strings.Contains(err.Error(), "live datapath disabled") {
		t.Fatalf("unexpected live startup result: %v", err)
	}
	select {
	case <-GracefullyExit:
	default:
		t.Fatal("exit notification missing")
	}
}
