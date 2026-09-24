package dae

import (
	"context"
	"errors"

	daeConfig "github.com/daeuniverse/dae/config"
)

// SubmitReload cannot strand a management request when the runtime has exited.
// The buffered result also lets the coordinator finish if a client times out.
// A timeout is not cancellation of an accepted transition; callers must refresh
// actual state rather than assume their requested configuration took effect.
func SubmitReload(ctx context.Context, conf *daeConfig.Config) error {
	reply := make(chan error, 1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-GracefullyExit:
		return errors.New("runtime is not running")
	case ChReloadConfigs <- &ReloadMessage{Config: conf, Callback: reply}:
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-GracefullyExit:
		select {
		case err := <-reply:
			return err
		default:
			return errors.New("runtime exited before reload acknowledgement")
		}
	}
}
