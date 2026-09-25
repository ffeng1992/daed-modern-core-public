/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package node

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/daeuniverse/dae-wing/common"
	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae/common/consts"
	dialer "github.com/daeuniverse/dae/component/outbound/dialer"
	"github.com/graph-gophers/graphql-go"
	"github.com/sirupsen/logrus"
)

const latencyProbeConcurrency = 8

func TestLatencies(ctx context.Context, ids *[]graphql.ID) ([]*LatencyResolver, error) {
	option, err := latencyProbeOption(ctx)
	if err != nil {
		return nil, err
	}

	nodes, err := latencyProbeNodes(ctx, ids)
	if err != nil {
		return nil, err
	}

	results := testLatencyResultsForNodes(option, nodes)
	storeLatencyResults(results)
	return results, nil
}

func testLatencyResultsForNodes(option *dialer.GlobalOption, nodes []db.Node) []*LatencyResolver {
	results := make([]*LatencyResolver, len(nodes))
	sem := make(chan struct{}, latencyProbeConcurrency)
	var wg sync.WaitGroup

	for index := range nodes {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			node := nodes[index]
			results[index] = testSingleNodeLatency(option, &node)
		}()
	}

	wg.Wait()
	return results
}

func latencyProbeOption(ctx context.Context) (*dialer.GlobalOption, error) {
	var configModel db.Config
	q := db.DB(ctx).Where("selected = ?", true).First(&configModel)
	if q.Error != nil {
		return nil, q.Error
	}

	parsedConfig, err := dae.ParseConfig(&configModel.Global, nil, nil)
	if err != nil {
		return nil, err
	}

	log := logrus.New()
	log.SetOutput(io.Discard)
	return dialer.NewGlobalOption(&parsedConfig.Global, log), nil
}

func latencyProbeNodes(ctx context.Context, ids *[]graphql.ID) ([]db.Node, error) {
	q := db.DB(ctx).Model(&db.Node{})
	if ids != nil {
		decodedIDs, err := common.DecodeCursorBatch(*ids)
		if err != nil {
			return nil, err
		}
		q = q.Where("id in ?", decodedIDs)
	}

	var nodes []db.Node
	if err := q.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func testSingleNodeLatency(option *dialer.GlobalOption, node *db.Node) *LatencyResolver {
	resolver := &LatencyResolver{
		NodeID:    node.ID,
		AliveVal:  false,
		TestedAtV: time.Now(),
	}

	d, err := dialer.NewFromLinkContext(context.Background(), option, dialer.InstanceOption{DisableCheck: false}, node.Link, "")
	if err != nil {
		msg := err.Error()
		resolver.MessageV = &msg
		return resolver
	}
	defer d.Close()

	// DAE v2.1.1 exposes asynchronous health observations rather than
	// the removed ProbeLatency method. Bound the wait and report an unknown
	// result if no TCP observation arrives; do not report a false failure.
	started := time.Now()
	d.ActivateCheck()
	d.NotifyCheckTcp()
	types := [2]dialer.NetworkType{
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_4},
		{L4Proto: consts.L4ProtoStr_TCP, IpVersion: consts.IpVersionStr_6},
	}
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var best *dialer.DialerProbeObservationSnapshot
		for i := range types {
			observation := d.SnapshotLastProbe(&types[i])
			if observation.CheckedAt.Before(started) {
				continue
			}
			if best == nil || (observation.Alive && !best.Alive) ||
				(observation.Alive == best.Alive && observation.Latency < best.Latency) {
				best = &observation
			}
		}
		if best != nil {
			resolver.AliveVal = best.Alive
			resolver.TestedAtV = best.CheckedAt
			if best.Alive && best.HasLatency {
				latencyMs := int32(best.Latency.Milliseconds())
				resolver.LatencyMsV = &latencyMs
			} else if best.Message != "" {
				resolver.MessageV = &best.Message
			}
			return resolver
		}
		select {
		case <-deadline.C:
			msg := "no TCP latency observation within 12 seconds"
			resolver.MessageV = &msg
			return resolver
		case <-ticker.C:
		}
	}
	return resolver
}
