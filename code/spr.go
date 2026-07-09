package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// The SPR API listens on localhost:80 (this container runs with host
// networking). Auth is the plugin install token SPR wrote to
// InstallTokenPath, scoped to the paths in plugin.json.
// SPR_API_BASE overrides for tests/dev outside the router.
var SPRAPIBase = envOr("SPR_API_BASE", "http://localhost:80")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var macRe = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

func normalizeMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}

func isMAC(s string) bool {
	return macRe.MatchString(normalizeMAC(s))
}

func sprToken() string {
	if tok, err := os.ReadFile(APITokenFile); err == nil {
		if t := strings.TrimSpace(string(tok)); t != "" {
			return t
		}
	}
	return configCopy().APIToken
}

func sprRequest(method, path string, body interface{}, out interface{}) error {
	cli := http.Client{Timeout: 15 * time.Second}
	defer cli.CloseIdleConnections()

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, SPRAPIBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+sprToken())
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("spr api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("spr api %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ---- SPR API types (JSON keys are exact Go field names, no tags) ----

type PSKEntry struct {
	Type string
	Psk  string
}

type DeviceStyle struct {
	Icon  string
	Color string
}

type DeviceEntry struct {
	Name              string
	MAC               string
	WGPubKey          string
	VLANTag           string
	RecentIP          string
	DNSCustom         string
	PSKEntry          PSKEntry
	Policies          []string
	Groups            []string
	DeviceTags        []string
	DHCPFirstTime     string
	DHCPLastTime      string
	DHCPLastInterface string
	Style             DeviceStyle
	DeviceExpiration  int64
	DeleteExpiration  bool
	DeviceDisabled    bool
}

type ExtraBSS struct {
	Ssid             string
	Bssid            string
	Wpa              string
	WpaKeyMgmt       string
	DisableIsolation bool
	GuestPassword    string
}

type InterfaceConfig struct {
	Name     string
	Type     string // "AP", "Uplink", "Other"
	Subtype  string
	Enabled  bool
	ExtraBSS []ExtraBSS `json:",omitempty"`
}

type ArpEntry struct {
	IP     string
	HWType string
	Flags  string
	MAC    string
	Mask   string
	Device string
}

type TrafficElement struct {
	IP      string
	Domain  string
	Packets uint64
	Bytes   uint64
}

type ReleaseInfo struct {
	CustomChannel string
	CustomVersion string
	Current       string
}

type UptimeInfo struct {
	UptimeTotalSeconds float64 `json:"uptime_total_seconds"`
	Load1m             float64 `json:"load_1m"`
	Load5m             float64 `json:"load_5m"`
	Load15m            float64 `json:"load_15m"`
	Users              int     `json:"users"`
}

// ip -j addr entries (kernel JSON)
type IPAddrInfo struct {
	Family    string `json:"family"`
	Local     string `json:"local"`
	Prefixlen int    `json:"prefixlen"`
	Scope     string `json:"scope"`
}

type IPAddr struct {
	Ifname    string       `json:"ifname"`
	Operstate string       `json:"operstate"`
	AddrInfo  []IPAddrInfo `json:"addr_info"`
}

// ---- fetch helpers ----

func fetchSPRDevices() (map[string]DeviceEntry, error) {
	devices := map[string]DeviceEntry{}
	if err := sprRequest("GET", "/devices", nil, &devices); err == nil {
		return devices, nil
	} else if data, ferr := os.ReadFile(DevicesPublicConfigFile); ferr == nil {
		// fall back to the public snapshot if the API is briefly down
		if json.Unmarshal(data, &devices) == nil {
			return devices, nil
		}
		return nil, err
	} else {
		return nil, err
	}
}

func fetchInterfaces() ([]InterfaceConfig, error) {
	var ifaces []InterfaceConfig
	err := sprRequest("GET", "/interfacesConfiguration", nil, &ifaces)
	return ifaces, err
}

func fetchStations(iface string) map[string]map[string]string {
	stations := map[string]map[string]string{}
	if err := sprRequest("GET", "/hostapd/"+iface+"/all_stations", nil, &stations); err != nil {
		return map[string]map[string]string{}
	}
	return stations
}

func fetchArp() []ArpEntry {
	var entries []ArpEntry
	if err := sprRequest("GET", "/arp", nil, &entries); err != nil {
		return nil
	}
	return entries
}

func fetchUptime() UptimeInfo {
	info := UptimeInfo{}
	_ = sprRequest("GET", "/info/uptime", nil, &info)
	return info
}

func fetchHostname() string {
	name := ""
	_ = sprRequest("GET", "/info/hostname", nil, &name)
	return name
}

func fetchVersion() string {
	versions := map[string]string{}
	if err := sprRequest("GET", "/version", nil, &versions); err != nil {
		return ""
	}
	if v, ok := versions["superd"]; ok {
		return v
	}
	for _, v := range versions {
		return v
	}
	return ""
}

func fetchRelease() ReleaseInfo {
	info := ReleaseInfo{}
	_ = sprRequest("GET", "/release", nil, &info)
	return info
}

func fetchTrafficSet(name string) []TrafficElement {
	var elements []TrafficElement
	if err := sprRequest("GET", "/traffic/"+name, nil, &elements); err != nil {
		return nil
	}
	return elements
}

func fetchIPAddrs() []IPAddr {
	var addrs []IPAddr
	if err := sprRequest("GET", "/ip/addr", nil, &addrs); err != nil {
		return nil
	}
	return addrs
}

// ---- actions ----

// hasWAN reports whether the device currently has internet access.
// Current SPR uses Policies; old releases used built-in Groups.
func hasWAN(dev DeviceEntry) bool {
	for _, p := range dev.Policies {
		if p == "wan" {
			return true
		}
	}
	if len(dev.Policies) == 0 {
		for _, g := range dev.Groups {
			if g == "wan" {
				return true
			}
		}
	}
	return false
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// sprSetDeviceBlocked cuts (or restores) a device's internet access by
// removing/adding the "wan" policy.
func sprSetDeviceBlocked(mac string, blocked bool) error {
	devices, err := fetchSPRDevices()
	if err != nil {
		return err
	}
	identity := ""
	var entry DeviceEntry
	for id, dev := range devices {
		if normalizeMAC(dev.MAC) == mac || normalizeMAC(id) == mac {
			identity, entry = id, dev
			break
		}
	}
	if identity == "" {
		return fmt.Errorf("unknown device %s", mac)
	}

	if blocked {
		entry.Policies = removeString(entry.Policies, "wan")
		entry.Groups = removeString(entry.Groups, "wan") // legacy releases
	} else if !hasWAN(entry) {
		entry.Policies = append(entry.Policies, "wan")
	}

	// Never write masked PSKs back
	entry.PSKEntry = PSKEntry{}

	return sprRequest("PUT", "/device?identity="+identity, entry, nil)
}

// sprSetGuestWifi toggles the guest SSID (extra BSS) on every enabled AP
// interface. Disabling stashes the BSS config in our plugin config so the
// same SSID/password comes back on enable.
func sprSetGuestWifi(enabled bool) error {
	ifaces, err := fetchInterfaces()
	if err != nil {
		return err
	}

	if enabled {
		saved := loadSavedGuestBSS()
		if len(saved) == 0 {
			return fmt.Errorf("no guest wifi configured: create one in the SPR UI first")
		}
		var lastErr error
		for _, iface := range ifaces {
			if iface.Type != "AP" || !iface.Enabled {
				continue
			}
			bss, ok := saved[iface.Name]
			if !ok {
				continue
			}
			if err := sprRequest("PUT", "/hostapd/"+iface.Name+"/enableExtraBSS", bss, nil); err != nil {
				lastErr = err
			}
		}
		return lastErr
	}

	// disabling: remember what was configured, then remove it
	saved := map[string]ExtraBSS{}
	var lastErr error
	for _, iface := range ifaces {
		if iface.Type != "AP" || len(iface.ExtraBSS) == 0 {
			continue
		}
		saved[iface.Name] = iface.ExtraBSS[0]
		if err := sprRequest("DELETE", "/hostapd/"+iface.Name+"/enableExtraBSS", nil, nil); err != nil {
			lastErr = err
		}
	}
	if len(saved) > 0 {
		saveGuestBSS(saved)
	}
	return lastErr
}

var GuestBSSFile = TEST_PREFIX + "/configs/plugins/home_assistant/guest_bss.json"

func loadSavedGuestBSS() map[string]ExtraBSS {
	saved := map[string]ExtraBSS{}
	if data, err := os.ReadFile(GuestBSSFile); err == nil {
		_ = json.Unmarshal(data, &saved)
	}
	return saved
}

func saveGuestBSS(saved map[string]ExtraBSS) {
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(GuestBSSFile, data, 0o600)
}

// sprRestart restarts the SPR service containers via superd.
func sprRestart() error {
	return sprRequest("PUT", "/restart", nil, nil)
}
