/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package dae

import (
	"context"
	"fmt"
	"net/netip"
	"runtime"

	"github.com/daeuniverse/dae-wing/compatconfig"
	daeCommon "github.com/daeuniverse/dae/common"
	"github.com/daeuniverse/dae/common/netutils"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/control"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"github.com/daeuniverse/outbound/protocol/direct"
	"github.com/mohae/deepcopy"
	"github.com/sirupsen/logrus"
)

var ErrControlPlaneNotInit = fmt.Errorf("control plane doesn't init yet")
var ErrAPIOnly = fmt.Errorf("API-only mode: datapath operations are unavailable")

type ReloadMessage struct {
	Config   *daeConfig.Config
	Callback chan<- error
}

var ChReloadConfigs = make(chan *ReloadMessage)
var GracefullyExit = make(chan struct{})
var EmptyConfig *daeConfig.Config
var managementSlot generationSlot

// Remains false until the v2 lifecycle integration passes isolated acceptance.
var liveDatapathValidated = false

func init() {
	sections, err := config_parser.Parse(`global{} routing{}`)
	if err != nil {
		panic(err)
	}
	EmptyConfig, err = daeConfig.New(sections)
	if err != nil {
		panic(err)
	}
}

// Run retains a production gate until the full forwarding/lifecycle matrix passes.
func Run(log *logrus.Logger, conf *daeConfig.Config, externGeoDataDirs []string, disableTimestamp bool, dry bool) error {
	defer close(GracefullyExit)
	if dry {
		for msg := range ChReloadConfigs {
			if msg == nil {
				return nil
			}
			msg.Callback <- ErrAPIOnly
		}
		return nil
	}
	if !liveDatapathValidated {
		return fmt.Errorf("experimental core integration: live datapath disabled until lifecycle acceptance; API-only dry mode is available")
	}
	return runCoordinator(log, conf, externGeoDataDirs, ChReloadConfigs)
}

func newControlPlane(log *logrus.Logger, bpf interface{}, dnsCache map[string]*control.DnsCache, conf *daeConfig.Config, externGeoDataDirs []string) (*control.ControlPlane, error) {
	return buildControlPlane(context.Background(), log, bpf, dnsCache, conf, externGeoDataDirs, false, false, bpf != nil)
}

// Prepared generations must not publish datapath state or bind DNS before the
// lifecycle coordinator has arranged ownership transfer from the active one.
func buildControlPlane(ctx context.Context, log *logrus.Logger, bpf interface{}, dnsCache map[string]*control.DnsCache, conf *daeConfig.Config, externGeoDataDirs []string, prepareOnly, dnsUnchanged, isReload bool) (c *control.ControlPlane, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conf == nil {
		return nil, fmt.Errorf("nil control plane configuration")
	}

	// Print configuration.
	if log.IsLevelEnabled(logrus.DebugLevel) {
		bConf, _ := conf.Marshal(2)
		log.Debugln(string(bConf))
	}

	// Deep copy to prevent modification.
	conf = deepcopy.Copy(conf).(*daeConfig.Config)
	conf.Global = compatconfig.RuntimeGlobal(conf.Global)

	conf.Global.SoMarkFromDae, _ = daeCommon.ResolveSoMarkFromDae(conf.Global.SoMarkFromDae, conf.Global.SoMarkFromDaeSet)
	// Init Direct Dialers.
	directDialers := direct.NewDirectDialers(conf.Global.FallbackResolver)
	fallback, err := netip.ParseAddrPort(conf.Global.FallbackResolver)
	if err != nil {
		return nil, fmt.Errorf("invalid fallback resolver: %w", err)
	}
	systemDNSResolver := netutils.NewSystemDNSResolver(fallback)

	if !conf.Global.DisableWaitingNetwork && len(conf.Global.WanInterface) > 0 {
		// Wait for network for WAN ready.
		if err := waitForNetwork(ctx, log, directDialers.Symmetric, conf.Global.SoMarkFromDae, conf.Global.Mptcp); err != nil {
			return nil, err
		}
	}

	/// Get subscription -> nodeList mapping.
	subscriptionToNodeList := map[string][]string{}
	if len(conf.Node) > 0 {
		for _, node := range conf.Node {
			subscriptionToNodeList[""] = append(subscriptionToNodeList[""], string(node))
		}
	}
	if len(conf.Subscription) > 0 {
		return nil, fmt.Errorf("daeConfig.subscription is not supported")
	}

	if err = preprocessWanInterfaceAuto(conf); err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// New dae control plane.
	c, err = control.NewControlPlaneWithContextOptions(
		ctx,
		log,
		bpf,
		dnsCache,
		subscriptionToNodeList,
		conf.Group,
		&conf.Routing,
		&conf.Global,
		&conf.Dns,
		externGeoDataDirs,
		control.ControlPlaneBuildOptions{
			DirectDialer:          directDialers.Symmetric,
			FullconeDirectDialer:  directDialers.Fullcone,
			SystemDNSResolver:     systemDNSResolver,
			DelayDatapathCommit:   prepareOnly,
			DelayDNSListenerStart: prepareOnly,
			DNSRoutingUnchanged:   dnsUnchanged,
			IsReload:              isReload || bpf != nil,
		},
	)
	if err != nil {
		return nil, err
	}
	// Call GC to release memory.
	runtime.GC()

	return c, nil
}
