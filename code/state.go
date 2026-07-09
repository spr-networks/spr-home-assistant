package main

import (
	"sync"
	"time"
)

// TrackedDevice is the merged view of an SPR device that we sync to
// Home Assistant. Presence is derived from wifi station lists, the ARP/
// neighbor table, and sprbus connect/disconnect events.
type TrackedDevice struct {
	MAC        string   `json:"mac"`
	Name       string   `json:"name"`
	IP         string   `json:"ip"`
	VLANTag    string   `json:"vlan_tag,omitempty"`
	Groups     []string `json:"groups"`
	DeviceTags []string `json:"tags"`

	Connected bool   `json:"connected"`
	Wired     bool   `json:"wired"`
	Iface     string `json:"iface,omitempty"`
	Signal    int    `json:"signal,omitempty"` // dBm, wifi only, 0 = unknown
	LastSeen  int64  `json:"last_seen"`        // unix seconds, 0 = never seen

	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`

	Blocked   bool `json:"blocked"`
	GuestWifi bool `json:"guest"`
}

type RouterInfo struct {
	Hostname         string `json:"hostname"`
	Model            string `json:"model,omitempty"`
	Version          string `json:"version"`
	LatestVersion    string `json:"latest_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	UptimeSeconds    int64   `json:"uptime_seconds"`
	Load1m           float64 `json:"load_1m"`
	Load5m           float64 `json:"load_5m"`
	Load15m          float64 `json:"load_15m"`
	WANIP            string `json:"wan_ip,omitempty"`
	WANIface         string `json:"wan_iface,omitempty"`
	WANUp            bool   `json:"wan_up"`
	GuestWifiEnabled bool   `json:"guest_wifi_enabled"`
	ClientsConnected int    `json:"clients_connected"`
}

type TrafficInfo struct {
	WANRxBytes uint64  `json:"wan_rx_bytes"`
	WANTxBytes uint64  `json:"wan_tx_bytes"`
	WANRxRate  float64 `json:"wan_rx_rate_bps"` // bits/sec, from the last poll delta
	WANTxRate  float64 `json:"wan_tx_rate_bps"`
}

// Snapshot is what GET /api/state returns to Home Assistant: everything the
// integration needs in a single poll.
type Snapshot struct {
	Router    RouterInfo      `json:"router"`
	Traffic   TrafficInfo     `json:"traffic"`
	Devices   []TrackedDevice `json:"devices"`
	Timestamp int64           `json:"timestamp"`
}

type stateStore struct {
	mtx      sync.RWMutex
	snapshot Snapshot
	// lastSeen persists device sightings across polls so a device missing
	// from one station-list read doesn't instantly flap to away.
	lastSeen map[string]int64

	prevWANRx, prevWANTx uint64
	prevTrafficAt        time.Time
}

var gState = &stateStore{lastSeen: map[string]int64{}}

func (s *stateStore) get() Snapshot {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	return s.snapshot
}

func (s *stateStore) markSeen(mac string, when time.Time) {
	s.mtx.Lock()
	s.lastSeen[normalizeMAC(mac)] = when.Unix()
	s.mtx.Unlock()
}

func (s *stateStore) set(snap Snapshot) {
	s.mtx.Lock()
	s.snapshot = snap
	s.mtx.Unlock()
}

// pollLoop refreshes the snapshot from SPR on a fixed interval. sprbus
// events refresh it immediately in between polls.
func pollLoop() {
	refreshState()
	interval := time.Duration(configCopy().PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-gRefreshNow:
		}
		refreshState()
	}
}

// gRefreshNow wakes the poll loop early (used by sprbus event handlers so
// connects/disconnects propagate to HA within a second, not a poll cycle).
var gRefreshNow = make(chan struct{}, 1)

func requestRefresh() {
	select {
	case gRefreshNow <- struct{}{}:
	default:
	}
}
