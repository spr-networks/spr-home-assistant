package main

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// refreshState pulls everything from SPR and swaps in a new snapshot.
// Wifi presence comes from hostapd station lists, wired presence from the
// ARP table; sprbus events keep lastSeen fresh between polls.
func refreshState() {
	now := time.Now()

	devices, err := fetchSPRDevices()
	if err != nil {
		log.Println("refresh: devices unavailable:", err)
		return
	}

	ifaces, err := fetchInterfaces()
	if err != nil {
		log.Println("refresh: interfaces unavailable:", err)
		ifaces = nil
	}

	guestEnabled := false
	wanIface := ""
	for _, iface := range ifaces {
		switch iface.Type {
		case "Uplink":
			if wanIface == "" || iface.Enabled {
				wanIface = iface.Name
			}
		case "AP":
			if len(iface.ExtraBSS) > 0 {
				guestEnabled = true
			}
		}
	}

	// presence: prefer SPR's own /topology merge; fall back to a local
	// hostapd+ARP merge on releases that don't have the endpoint
	topo, topoOK := fetchTopologyPresence()

	type stationInfo struct {
		iface   string
		signal  int
		rxBytes uint64
		txBytes uint64
	}
	stations := map[string]stationInfo{}
	arpSeen := map[string]string{} // mac -> iface, learned entries only

	if !topoOK {
		for _, iface := range ifaces {
			if iface.Type != "AP" || !iface.Enabled {
				continue
			}
			names := []string{iface.Name}
			if len(iface.ExtraBSS) > 0 {
				names = append(names, iface.Name+".ap0")
			}
			for _, name := range names {
				for mac, sta := range fetchStations(name) {
					mac = normalizeMAC(mac)
					if !isMAC(mac) {
						continue
					}
					info := stationInfo{iface: name}
					info.signal, _ = strconv.Atoi(sta["signal"])
					info.rxBytes, _ = strconv.ParseUint(sta["rx_bytes"], 10, 64)
					info.txBytes, _ = strconv.ParseUint(sta["tx_bytes"], 10, 64)
					stations[mac] = info
				}
			}
		}

		for _, entry := range fetchArp() {
			if entry.Device == wanIface {
				continue
			}
			flags, err := strconv.ParseInt(strings.TrimPrefix(entry.Flags, "0x"), 16, 32)
			// need a learned, complete entry (0x2): SPR seeds permanent
			// (0x4) entries for known devices, which say nothing about
			// presence
			if err != nil || flags&0x2 == 0 || flags&0x4 != 0 {
				continue
			}
			mac := normalizeMAC(entry.MAC)
			if isMAC(mac) {
				arpSeen[mac] = entry.Device
			}
		}
	}

	// cumulative per-LAN-IP WAN counters from nft accounting
	rxByIP := map[string]uint64{} // wan -> device (download)
	txByIP := map[string]uint64{} // device -> wan (upload)
	var wanRxTotal, wanTxTotal uint64
	for _, el := range fetchTrafficSet("incoming_traffic_wan") {
		rxByIP[el.IP] += el.Bytes
		wanRxTotal += el.Bytes
	}
	for _, el := range fetchTrafficSet("outgoing_traffic_wan") {
		txByIP[el.IP] += el.Bytes
		wanTxTotal += el.Bytes
	}

	wanIP, wanUp := "", false
	for _, addr := range fetchIPAddrs() {
		if addr.Ifname != wanIface {
			continue
		}
		wanUp = strings.EqualFold(addr.Operstate, "UP")
		for _, info := range addr.AddrInfo {
			if info.Family == "inet" && info.Scope == "global" {
				wanIP = info.Local
				break
			}
		}
		break
	}

	uptime := fetchUptime()
	release := fetchRelease()
	version := fetchVersion()
	if version == "" {
		version = release.Current
	}
	latest := latestAvailableVersion(version)

	// copy under lock: sprbus events write gState.lastSeen concurrently
	gState.mtx.Lock()
	lastSeen := make(map[string]int64, len(gState.lastSeen))
	for mac, ts := range gState.lastSeen {
		lastSeen[mac] = ts
	}
	prevConnected := map[string]bool{}
	for _, d := range gState.snapshot.Devices {
		prevConnected[d.MAC] = d.Connected
	}
	gState.mtx.Unlock()

	tracked := []TrackedDevice{}
	connectedCount := 0
	for identity, dev := range devices {
		mac := normalizeMAC(dev.MAC)
		if mac == "" {
			mac = normalizeMAC(identity)
		}
		if !isMAC(mac) {
			continue // "pending" placeholder or VPN-only device
		}

		td := TrackedDevice{
			MAC:        mac,
			Name:       dev.Name,
			IP:         dev.RecentIP,
			VLANTag:    dev.VLANTag,
			Groups:     dev.Groups,
			DeviceTags: dev.DeviceTags,
			Blocked:    !hasWAN(dev),
		}
		for _, tag := range dev.DeviceTags {
			if tag == "guest" {
				td.GuestWifi = true
			}
		}
		for _, p := range dev.Policies {
			if p == "guestonly" {
				td.GuestWifi = true
			}
		}

		if topoOK {
			if p, ok := topo[mac]; ok && p.online {
				td.Connected = true
				td.Wired = p.connType == "wired"
				td.Iface = p.iface
				td.Signal = p.signal
			}
		} else if sta, ok := stations[mac]; ok {
			td.Connected = true
			td.Iface = sta.iface
			td.Signal = sta.signal
			td.RxBytes = sta.rxBytes
			td.TxBytes = sta.txBytes
		} else if iface, ok := arpSeen[mac]; ok {
			td.Connected = true
			td.Wired = true
			td.Iface = iface
		}

		// nft per-IP counters survive wifi reassociation; prefer them
		if td.IP != "" {
			if rx := rxByIP[td.IP]; rx > 0 {
				td.RxBytes = rx
			}
			if tx := txByIP[td.IP]; tx > 0 {
				td.TxBytes = tx
			}
		}

		if td.Connected {
			lastSeen[mac] = now.Unix()
		}
		td.LastSeen = lastSeen[mac]

		if td.Connected {
			connectedCount++
		}
		tracked = append(tracked, td)
	}
	sort.Slice(tracked, func(i, j int) bool { return tracked[i].MAC < tracked[j].MAC })

	snap := Snapshot{
		Router: RouterInfo{
			Hostname:         fetchHostname(),
			Version:          version,
			LatestVersion:    latest,
			UpdateAvailable:  latest != "" && version != "" && semverLess(version, latest),
			UptimeSeconds:    int64(uptime.UptimeTotalSeconds),
			Load1m:           uptime.Load1m,
			Load5m:           uptime.Load5m,
			Load15m:          uptime.Load15m,
			WANIP:            wanIP,
			WANIface:         wanIface,
			WANUp:            wanUp,
			GuestWifiEnabled: guestEnabled,
			ClientsConnected: connectedCount,
		},
		Traffic:   TrafficInfo{WANRxBytes: wanRxTotal, WANTxBytes: wanTxTotal},
		Devices:   tracked,
		Timestamp: now.Unix(),
	}

	gState.mtx.Lock()
	// merge sightings back without clobbering fresher event-driven updates
	for mac, ts := range lastSeen {
		if ts > gState.lastSeen[mac] {
			gState.lastSeen[mac] = ts
		}
	}
	if !gState.prevTrafficAt.IsZero() {
		dt := now.Sub(gState.prevTrafficAt).Seconds()
		if dt > 0 && wanRxTotal >= gState.prevWANRx && wanTxTotal >= gState.prevWANTx {
			snap.Traffic.WANRxRate = float64(wanRxTotal-gState.prevWANRx) * 8 / dt
			snap.Traffic.WANTxRate = float64(wanTxTotal-gState.prevWANTx) * 8 / dt
		}
	}
	gState.prevWANRx, gState.prevWANTx = wanRxTotal, wanTxTotal
	gState.prevTrafficAt = now
	gState.snapshot = snap
	gState.mtx.Unlock()

	// push connect/disconnect transitions to Home Assistant over SSE
	for _, d := range tracked {
		was, known := prevConnected[d.MAC]
		if !known || was == d.Connected {
			continue
		}
		event := "device_disconnected"
		if d.Connected {
			event = "device_connected"
		}
		gHub.broadcast(event, map[string]interface{}{"mac": d.MAC, "name": d.Name, "ip": d.IP})
	}
}
