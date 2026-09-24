/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package dae

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/daeuniverse/dae/cmd"
	daeCommon "github.com/daeuniverse/dae/common"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"github.com/daeuniverse/outbound/netproxy"
	"github.com/sirupsen/logrus"
)

var (
	EmptyGroupSection        = `group {}`
	EmptySubscriptionSection = `subscription {}`
	EmptyNodeSection         = `node {}`
	EmptyRoutingSection      = `routing {}`
	EmptyDnsSection          = `dns {}`
	EmptyGlobalSection       = `global {}`
)

func NecessaryOutbounds(routing *daeConfig.Routing) (outbounds []string, err error) {
	if routing == nil {
		return nil, fmt.Errorf("routing is nil")
	}
	f, err := daeConfig.ParseFunctionOrString(routing.Fallback)
	if err != nil {
		return nil, err
	}
	outbounds = append(outbounds, f.Name)
	for _, r := range routing.Rules {
		outbound := r.Outbound.Name
		if outbound != "must_rules" {
			outbound = strings.TrimPrefix(outbound, "must_")
		}
		outbounds = append(outbounds, outbound)
	}
	return daeCommon.Deduplicate(outbounds), nil
}

func ParseConfig(globalSection *string, dnsSection *string, routingSection *string) (*daeConfig.Config, error) {
	if globalSection == nil {
		globalSection = &EmptyGlobalSection
	}
	if dnsSection == nil {
		dnsSection = &EmptyDnsSection
	}
	if routingSection == nil {
		routingSection = &EmptyRoutingSection
	}
	strConfig := strings.Join([]string{
		*globalSection,
		*dnsSection,
		*routingSection,
		EmptyGroupSection,
		EmptySubscriptionSection,
		EmptyNodeSection,
	}, "\n")
	// Parse it to sections.
	sections, err := config_parser.Parse(strConfig)
	if err != nil {
		return nil, err
	}
	// New dae.Config from sections.
	c, err := daeConfig.New(sections)
	if err != nil {
		return nil, err
	}
	return c, err
}

func preprocessWanInterfaceAuto(params *daeConfig.Config) error {
	// preprocess "auto".
	ifs := make([]string, 0, len(params.Global.WanInterface)+2)
	for _, ifname := range params.Global.WanInterface {
		if ifname == "auto" {
			defaultIfs, err := daeCommon.GetDefaultIfnames()
			if err != nil {
				return fmt.Errorf("failed to convert 'auto': %w", err)
			}
			ifs = append(ifs, defaultIfs...)
		} else {
			ifs = append(ifs, ifname)
		}
	}
	params.Global.WanInterface = daeCommon.Deduplicate(ifs)
	return nil
}

// Bound preparation and let shutdown/reload cancellation interrupt HTTP or retry waits.
func waitForNetwork(ctx context.Context, log *logrus.Logger, dialer netproxy.Dialer, mark uint32, mptcp bool) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, daeCommon.MagicNetwork(network, mark, mptcp), addr)
		if err != nil {
			return nil, err
		}
		return &netproxy.FakeNetConn{Conn: conn}, nil
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	return probeNetwork(ctx, client, cmd.CheckNetworkLinks, 5*time.Second, log)
}

func probeNetwork(ctx context.Context, client *http.Client, links []string, retry time.Duration, log *logrus.Logger) error {
	if len(links) == 0 {
		return fmt.Errorf("no network probe URLs")
	}
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, links[i%len(links)], nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 500 {
			return nil
		}
		if err != nil {
			log.Debug("Network probe failed")
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
