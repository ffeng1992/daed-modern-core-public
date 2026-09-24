//go:build linux && !dae_stub_ebpf

package dae

import (
	"errors"
	"github.com/sirupsen/logrus"
	"os"
)

// Test-only entrypoint: not included in any daemon binary or production API.
func RunIsolatedCoordinatorForTest() error {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DAED_ISOLATED_ACCEPTANCE") != "1" || os.Geteuid() != 0 {
		return errors.New("isolated CI only")
	}
	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)
	defer close(GracefullyExit)
	return runCoordinator(log, EmptyConfig, nil, ChReloadConfigs)
}
