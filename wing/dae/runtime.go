/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package dae

import (
	"errors"
	"github.com/daeuniverse/dae/control"
	"time"
)

type RuntimeTrafficSample struct {
	Timestamp    time.Time
	UploadRate   uint64
	DownloadRate uint64
}

// SnapshotNodeLatencies pins the active generation for the duration of the read.
func SnapshotNodeLatencies() ([]control.NodeLatencySnapshot, error) {
	ctl, release, err := managementSlot.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	return ctl.SnapshotNodeLatencies(), nil
}

// TriggerLatencyChecks avoids exposing a control plane after its lease ends.
func TriggerLatencyChecks() {
	ctl, release, err := managementSlot.acquire()
	if err != nil {
		return
	}
	defer release()
	ctl.TriggerLatencyChecks()
}

type RuntimeOverview struct {
	UpdatedAt         time.Time
	UploadRate        uint64
	DownloadRate      uint64
	UploadTotal       uint64
	DownloadTotal     uint64
	ActiveConnections int
	UDPSessions       int
	Samples           []RuntimeTrafficSample
}

func GetRuntimeOverview(windowSec int, maxPoints int) (*RuntimeOverview, error) {
	ctl, release, err := managementSlot.acquire()
	if err != nil {
		if !errors.Is(err, ErrControlPlaneNotInit) {
			return nil, err
		}
		// Preserve the upstream off-state overview without borrowing a retired plane.
		ctl = nil
	} else {
		defer release()
	}

	snapshot := ctl.SnapshotRuntimeStats(windowSec, maxPoints)

	samples := make([]RuntimeTrafficSample, 0, len(snapshot.Samples))
	for _, sample := range snapshot.Samples {
		samples = append(samples, RuntimeTrafficSample{
			Timestamp:    sample.Timestamp,
			UploadRate:   sample.UploadRate,
			DownloadRate: sample.DownloadRate,
		})
	}

	return &RuntimeOverview{
		UpdatedAt:         snapshot.UpdatedAt,
		UploadRate:        snapshot.UploadRate,
		DownloadRate:      snapshot.DownloadRate,
		UploadTotal:       snapshot.UploadTotal,
		DownloadTotal:     snapshot.DownloadTotal,
		ActiveConnections: snapshot.ActiveConnections,
		UDPSessions:       snapshot.UDPSessions,
		Samples:           samples,
	}, nil
}
