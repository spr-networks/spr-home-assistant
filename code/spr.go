package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// SPRAPIBase points at the SPR API. Auth is the read-only install token SPR
// wrote to InstallTokenPath, scoped to the paths in plugin.json.
// SPR_API_BASE overrides for tests/dev outside the router.
var SPRAPIBase = "http://127.0.0.1:80"

// getGateway finds the SPR API host. On virtual SPR the plugin runs in the
// service:base network namespace, so it's localhost; in a regular container
// network it's the container's default gateway (the SPR host).
func getGateway() (string, error) {
	if os.Getenv("VIRTUAL_SPR") == "1" {
		return "127.0.0.1", nil
	}

	out, err := exec.Command("ip", "route").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "default") {
			fields := strings.Fields(line)
			if len(fields) > 2 {
				return fields[2], nil
			}
		}
	}
	return "", fmt.Errorf("gateway not found")
}

// initSPRAPIBase resolves the API base once at startup.
func initSPRAPIBase() {
	if base := os.Getenv("SPR_API_BASE"); base != "" {
		SPRAPIBase = base
		return
	}
	gw, err := getGateway()
	if err != nil {
		log.Println("gateway detection failed, using localhost:", err)
		return
	}
	SPRAPIBase = "http://" + net.JoinHostPort(gw, "80")
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

// hasWAN reports whether the device currently has internet access, so we can
// expose a read-only "blocked" attribute. Current SPR uses Policies; old
// releases used built-in Groups.
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
