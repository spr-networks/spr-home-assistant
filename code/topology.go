package main

// Presence via SPR's /topology endpoint: it already merges hostapd station
// lists, learned-vs-seeded ARP entries, and wireguard state with the same
// logic the SPR UI uses. Preferred over our legacy merge when available.

type TopoSignal struct {
	RSSI   int
	TxRate int
	RxRate int
}

type TopoNode struct {
	ID       string
	Kind     string // router | uplink | ap_radio | port | vpn | device | ...
	Name     string
	MAC      string
	IP       string
	VLANTag  string
	ConnType string // wifi | wired | wireguard | offline
	Iface    string
	Groups   []string
	Policies []string
	Tags     []string
	Signal   *TopoSignal
	Online   bool
}

type topoResponse struct {
	Nodes []TopoNode
}

type topoPresence struct {
	online   bool
	connType string
	iface    string
	signal   int
}

// fetchTopologyPresence returns presence keyed by MAC, or ok=false when the
// endpoint is unavailable (older SPR releases) so callers fall back to the
// legacy hostapd+ARP merge.
func fetchTopologyPresence() (map[string]topoPresence, bool) {
	var topo topoResponse
	if err := sprRequest("GET", "/topology", nil, &topo); err != nil {
		return nil, false
	}
	if len(topo.Nodes) == 0 {
		return nil, false
	}

	presence := map[string]topoPresence{}
	for _, node := range topo.Nodes {
		if node.Kind != "device" {
			continue
		}
		mac := normalizeMAC(node.MAC)
		if !isMAC(mac) {
			continue
		}
		entry := topoPresence{
			online:   node.Online,
			connType: node.ConnType,
			iface:    node.Iface,
		}
		if node.Signal != nil {
			entry.signal = node.Signal.RSSI
		}
		presence[mac] = entry
	}
	return presence, true
}
