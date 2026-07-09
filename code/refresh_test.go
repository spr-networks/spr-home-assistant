package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockSPR serves the subset of the SPR API that ha_sync consumes.
func mockSPR(t *testing.T) (*httptest.Server, *map[string]DeviceEntry) {
	t.Helper()
	devices := map[string]DeviceEntry{
		"aa:bb:cc:dd:ee:01": {
			Name: "phone", MAC: "aa:bb:cc:dd:ee:01", RecentIP: "192.168.2.100",
			Policies: []string{"wan", "dns"}, DeviceTags: []string{},
		},
		"aa:bb:cc:dd:ee:02": {
			Name: "printer", MAC: "aa:bb:cc:dd:ee:02", RecentIP: "192.168.2.101",
			Policies: []string{"dns"}, DeviceTags: []string{"guest"},
		},
		"pending": {Name: "", MAC: ""},
	}

	mux := http.NewServeMux()
	writeAs := func(v interface{}) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
	mux.HandleFunc("/devices", writeAs(devices))
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method", 405)
			return
		}
		identity := r.URL.Query().Get("identity")
		var entry DeviceEntry
		if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		devices[identity] = entry
		_ = json.NewEncoder(w).Encode(entry)
	})
	mux.HandleFunc("/interfacesConfiguration", writeAs([]InterfaceConfig{
		{Name: "wlan0", Type: "AP", Enabled: true,
			ExtraBSS: []ExtraBSS{{Ssid: "guests", Wpa: "2"}}},
		{Name: "eth0", Type: "Uplink", Enabled: true},
	}))
	mux.HandleFunc("/hostapd/wlan0/all_stations", writeAs(map[string]map[string]string{
		"aa:bb:cc:dd:ee:01": {"signal": "-52", "rx_bytes": "1000", "tx_bytes": "2000"},
	}))
	mux.HandleFunc("/hostapd/wlan0.ap0/all_stations", writeAs(map[string]map[string]string{}))
	mux.HandleFunc("/hostapd/wlan0/enableExtraBSS", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut && r.Method != http.MethodDelete {
			http.Error(w, "method", 405)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	mux.HandleFunc("/arp", writeAs([]ArpEntry{
		{IP: "192.168.2.101", MAC: "aa:bb:cc:dd:ee:02", Flags: "0x2", Device: "eth1"},
		{IP: "1.2.3.4", MAC: "aa:bb:cc:dd:ee:99", Flags: "0x2", Device: "eth0"}, // wan side, ignored
	}))
	mux.HandleFunc("/info/uptime", writeAs(map[string]interface{}{
		"uptime_total_seconds": 3600.0, "load_1m": 0.5, "load_5m": 0.4, "load_15m": 0.3,
	}))
	mux.HandleFunc("/info/hostname", writeAs("spr"))
	mux.HandleFunc("/version", writeAs(map[string]string{"superd": "1.0.0"}))
	mux.HandleFunc("/release", writeAs(ReleaseInfo{Current: "1.0.0"}))
	mux.HandleFunc("/releasesAvailable", writeAs([]string{"1.0.0", "1.0.1", "latest", "1.0.1-dev"}))
	mux.HandleFunc("/traffic/incoming_traffic_wan", writeAs([]TrafficElement{
		{IP: "192.168.2.100", Bytes: 5000},
		{IP: "192.168.2.101", Bytes: 100},
	}))
	mux.HandleFunc("/traffic/outgoing_traffic_wan", writeAs([]TrafficElement{
		{IP: "192.168.2.100", Bytes: 700},
	}))
	mux.HandleFunc("/ip/addr", writeAs([]IPAddr{
		{Ifname: "eth0", Operstate: "UP", AddrInfo: []IPAddrInfo{
			{Family: "inet", Local: "203.0.113.7", Scope: "global"},
		}},
	}))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &devices
}

func setupTestEnv(t *testing.T, server *httptest.Server) {
	t.Helper()
	oldBase := SPRAPIBase
	SPRAPIBase = server.URL
	dir := t.TempDir()
	oldToken, oldGuest, oldConfig := APITokenFile, GuestBSSFile, ConfigFile
	APITokenFile = filepath.Join(dir, "api-token")
	GuestBSSFile = filepath.Join(dir, "guest_bss.json")
	ConfigFile = filepath.Join(dir, "config.json")
	_ = os.WriteFile(APITokenFile, []byte("test-token\n"), 0o600)
	gState.mtx.Lock()
	gState.snapshot = Snapshot{}
	gState.lastSeen = map[string]int64{}
	gState.prevTrafficAt = time.Time{}
	gState.prevWANRx, gState.prevWANTx = 0, 0
	gState.mtx.Unlock()
	versionCheckMtx.Lock()
	versionCheckAt = time.Time{}
	versionCheckResult = ""
	versionCheckMtx.Unlock()
	t.Cleanup(func() {
		SPRAPIBase = oldBase
		APITokenFile, GuestBSSFile, ConfigFile = oldToken, oldGuest, oldConfig
	})
}

func TestRefreshState(t *testing.T) {
	server, _ := mockSPR(t)
	setupTestEnv(t, server)

	refreshState()
	snap := gState.get()

	if len(snap.Devices) != 2 {
		t.Fatalf("got %d devices, want 2 (pending skipped): %+v", len(snap.Devices), snap.Devices)
	}

	byMAC := map[string]TrackedDevice{}
	for _, d := range snap.Devices {
		byMAC[d.MAC] = d
	}

	phone := byMAC["aa:bb:cc:dd:ee:01"]
	if !phone.Connected || phone.Wired || phone.Iface != "wlan0" || phone.Signal != -52 {
		t.Errorf("phone presence wrong: %+v", phone)
	}
	if phone.RxBytes != 5000 || phone.TxBytes != 700 {
		t.Errorf("phone counters should prefer nft per-IP: %+v", phone)
	}
	if phone.Blocked {
		t.Error("phone has wan policy, should not be blocked")
	}

	printer := byMAC["aa:bb:cc:dd:ee:02"]
	if !printer.Connected || !printer.Wired || printer.Iface != "eth1" {
		t.Errorf("printer should be wired-connected via arp: %+v", printer)
	}
	if !printer.Blocked {
		t.Error("printer lacks wan policy, should show blocked")
	}
	if !printer.GuestWifi {
		t.Error("printer tagged guest, should show guest")
	}

	r := snap.Router
	if r.Hostname != "spr" || r.Version != "1.0.0" || !r.WANUp ||
		r.WANIP != "203.0.113.7" || r.WANIface != "eth0" {
		t.Errorf("router info wrong: %+v", r)
	}
	if !r.GuestWifiEnabled {
		t.Error("guest wifi should read enabled (ExtraBSS present)")
	}
	if r.ClientsConnected != 2 {
		t.Errorf("clients connected = %d, want 2", r.ClientsConnected)
	}
	if r.LatestVersion != "1.0.1" || !r.UpdateAvailable {
		t.Errorf("update check wrong: latest=%q available=%v", r.LatestVersion, r.UpdateAvailable)
	}
	if snap.Traffic.WANRxBytes != 5100 || snap.Traffic.WANTxBytes != 700 {
		t.Errorf("wan totals wrong: %+v", snap.Traffic)
	}
	if r.UptimeSeconds != 3600 || r.Load1m != 0.5 {
		t.Errorf("uptime/load wrong: %+v", r)
	}
}

func TestSetDeviceBlocked(t *testing.T) {
	server, devices := mockSPR(t)
	setupTestEnv(t, server)

	if err := sprSetDeviceBlocked("aa:bb:cc:dd:ee:01", true); err != nil {
		t.Fatal(err)
	}
	entry := (*devices)["aa:bb:cc:dd:ee:01"]
	for _, p := range entry.Policies {
		if p == "wan" {
			t.Fatal("wan policy not removed")
		}
	}
	if entry.PSKEntry.Psk != "" || entry.PSKEntry.Type != "" {
		t.Error("PSK must be scrubbed before writing back")
	}

	if err := sprSetDeviceBlocked("aa:bb:cc:dd:ee:01", false); err != nil {
		t.Fatal(err)
	}
	entry = (*devices)["aa:bb:cc:dd:ee:01"]
	if !hasWAN(entry) {
		t.Fatal("wan policy not restored")
	}

	if err := sprSetDeviceBlocked("00:00:00:00:00:99", true); err == nil {
		t.Error("expected error for unknown device")
	}
}

func TestGuestWifiRoundTrip(t *testing.T) {
	server, _ := mockSPR(t)
	setupTestEnv(t, server)

	// disable stashes the BSS config
	if err := sprSetGuestWifi(false); err != nil {
		t.Fatal("disable:", err)
	}
	saved := loadSavedGuestBSS()
	if bss, ok := saved["wlan0"]; !ok || bss.Ssid != "guests" {
		t.Fatalf("guest BSS not stashed: %+v", saved)
	}

	// enable restores it (mock keeps reporting ExtraBSS present, fine here)
	if err := sprSetGuestWifi(true); err != nil {
		t.Fatal("enable:", err)
	}
}

func TestHAAuth(t *testing.T) {
	server, _ := mockSPR(t)
	setupTestEnv(t, server)
	loadConfig()

	handler := requireAuth(handleState)

	req := httptest.NewRequest("GET", "/api/state", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", rec.Code)
	}

	req = httptest.NewRequest("GET", "/api/state", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", rec.Code)
	}

	req = httptest.NewRequest("GET", "/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+configCopy().HAToken)
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "devices") {
		t.Error("state response missing devices")
	}
}

func TestProbeUnauthenticated(t *testing.T) {
	server, _ := mockSPR(t)
	setupTestEnv(t, server)
	loadConfig()

	rec := httptest.NewRecorder()
	handleProbe(rec, httptest.NewRequest("GET", "/api/probe", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("probe: got %d", rec.Code)
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatal(err)
	}
	if probe["product"] != "spr" || probe["id"] == "" {
		t.Errorf("probe malformed: %v", probe)
	}
	if _, ok := probe["hostname"]; !ok {
		t.Error("probe missing hostname")
	}
}
