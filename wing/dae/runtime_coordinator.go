package dae

import (
	"context"
	"errors"
	"fmt"
	"time"

	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/control"
	"github.com/mohae/deepcopy"
	"github.com/sirupsen/logrus"
)

// The first integrated coordinator deliberately uses stop/rebuild, not an
// unproven overlapping BPF handoff. Reload interrupts existing flows and has a
// DNS gap. A failed candidate is rebuilt from the last successful configuration.
// This is an isolated acceptance candidate, not a seamless reload claim.
var errRuntimeCleanup = errors.New("runtime cleanup incomplete")

type runtimeInstance struct {
	plane    *control.ControlPlane
	listener *control.Listener
	life     *serveLifecycle
	cancel   context.CancelFunc
	lease    *activeGeneration
	conf     *daeConfig.Config
}

func startRuntime(log *logrus.Logger, conf *daeConfig.Config, dirs []string) (*runtimeInstance, error) {
	if conf == nil {
		return nil, errors.New("nil runtime configuration")
	}
	// Build context has no startup deadline: a deadline retained by the core
	// would kill an otherwise healthy generation after startup.
	ctx, cancel := context.WithCancel(context.Background())
	startupTimer := time.AfterFunc(15*time.Second, cancel)
	defer startupTimer.Stop()
	r := &runtimeInstance{cancel: cancel, conf: deepcopy.Copy(conf).(*daeConfig.Config)}
	var err error
	r.plane, err = buildControlPlane(ctx, log, nil, nil, r.conf, dirs, true, false, false)
	if err != nil {
		cancel()
		return nil, err
	}
	err = control.GetDaeNetns().WithRequired("bridge runtime listener", func() error {
		var e error
		r.listener, e = r.plane.Listen(r.conf.Global.TproxyPort)
		return e
	})
	if err == nil {
		r.life = startServing(func(ready chan<- bool) error { return r.plane.Serve(ready, r.listener) })
		readyCtx, readyCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = r.life.waitReady(readyCtx)
		readyCancel()
	}
	if err == nil {
		// Prepared Serve publishes sockets/routing/DNS, but deliberately leaves
		// interface hooks for the supervisor. A ready DNS listener alone is
		// not a ready transparent proxy.
		err = r.plane.CommitPreparedBpfHookFlip()
	}
	if !startupTimer.Stop() && err == nil {
		err = errors.New("runtime startup budget expired")
	}
	if err != nil {
		cleanupErr := r.close()
		if cleanupErr != nil {
			return nil, errors.Join(err, errRuntimeCleanup, cleanupErr)
		}
		if r.life != nil {
			select {
			case <-r.life.done:
				err = errors.Join(err, r.life.err)
			default:
			}
		}
		return nil, err
	}
	return r, nil
}

func (r *runtimeInstance) close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.listener != nil {
		errs = append(errs, r.listener.Close())
	}
	if r.plane != nil {
		errs = append(errs, r.plane.DetachBpfHooks())
		// Drain DNS before destroying its namespace or entering the core's
		// five-second deferred-cleanup budget. An unavailable upstream can
		// legitimately consume the DNS controller's own shutdown budget.
		// This bridge never shares a DNS controller across generations.
		if r.cancel != nil {
			r.cancel()
		}
		if dnsController := r.plane.ActiveDnsController(); dnsController != nil {
			errs = append(errs, dnsController.Close())
		}
		errs = append(errs, r.plane.StopDNSListener())
		// Match orderly official shutdown: tear down the namespace before the
		// owning BPF maps. No second instance may be constructed until completion.
		errs = append(errs, control.GetDaeNetns().Close())
		errs = append(errs, r.plane.AbortConnections())
		errs = append(errs, r.plane.Close())
		// Cold rebuild destroys dae's network namespace. Process-global reply
		// sockets must not survive it; otherwise later UDP replies reuse a
		// socket in the detached namespace and fail ENETUNREACH.
		// Do not call ResetGlobalUdpState here: that also stops shared janitors.
		control.DefaultAnyfromPool.Reset()
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.life != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = r.life.waitExit(ctx)
		// Serve failure is the transition error, not a cleanup failure. Only
		// an unfinished exit prevents safe reuse of runtime resources.
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
		}
		cancel()
	}
	return errors.Join(errs...)
}

// Quiesce management users before changing any kernel state. A timeout republishes
// the old plane without closing it, so callers get an error but service survives.
func unpublishRuntime(r *runtimeInstance) error {
	if r == nil {
		return nil
	}
	if _, err := managementSlot.publish(r.lease, nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := r.lease.waitManagementDrain(ctx)
	cancel()
	if err != nil {
		// Restore the same generation, not a fresh counter: outstanding users still
		// release this original lease. No teardown has started.
		if restoreErr := managementSlot.restore(r.lease); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return fmt.Errorf("management users did not drain; runtime unchanged: %w", err)
	}
	return nil
}

func runCoordinator(log *logrus.Logger, initial *daeConfig.Config, dirs []string, requests <-chan *ReloadMessage) (result error) {
	var active *runtimeInstance
	var last *daeConfig.Config
	publish := func(r *runtimeInstance) error {
		var err error
		r.lease, err = managementSlot.publish(nil, r.plane)
		if err == nil {
			active = r
			last = r.conf
		}
		return err
	}
	if initial != EmptyConfig {
		r, err := startRuntime(log, initial, dirs)
		if err != nil {
			return err
		}
		if err = publish(r); err != nil {
			return errors.Join(err, r.close())
		}
	}
	defer func() {
		if active != nil {
			if err := unpublishRuntime(active); err != nil {
				result = errors.Join(result, err)
				return
			}
			result = errors.Join(result, active.close())
		}
	}()
	for {
		var exited <-chan struct{}
		if active != nil {
			exited = active.life.done
		}
		select {
		case <-exited:
			return fmt.Errorf("active runtime exited: %w", errors.Join(errServeBeforeReady, active.life.err))
		case msg, ok := <-requests:
			if !ok || msg == nil {
				return nil
			}
			if msg.Config == nil {
				msg.Callback <- errors.New("nil reload configuration")
				continue
			}
			previous := last
			if active != nil {
				if err := unpublishRuntime(active); err != nil {
					msg.Callback <- err
					continue
				}
				err := active.close()
				active = nil
				if err != nil {
					msg.Callback <- fmt.Errorf("runtime cleanup failed: %w", err)
					return err
				}
			}
			if msg.Config == EmptyConfig {
				last = nil
				msg.Callback <- nil
				continue
			}
			next, err := startRuntime(log, msg.Config, dirs)
			if err == nil {
				err = publish(next)
				if err != nil {
					err = errors.Join(err, next.close())
				}
			}
			if errors.Is(err, errRuntimeCleanup) {
				msg.Callback <- err
				return err
			}
			if err != nil && previous != nil {
				restored, restoreErr := startRuntime(log, previous, dirs)
				if restoreErr == nil {
					restoreErr = publish(restored)
					if restoreErr != nil {
						restoreErr = errors.Join(restoreErr, restored.close())
					}
				}
				if restoreErr != nil {
					failure := errors.Join(err, fmt.Errorf("restore previous configuration: %w", restoreErr))
					msg.Callback <- failure
					return failure
				}
			}
			msg.Callback <- err
		}
	}
}
