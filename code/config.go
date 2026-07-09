package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var TEST_PREFIX = os.Getenv("TEST_PREFIX")

var UNIX_PLUGIN_LISTENER = TEST_PREFIX + "/state/plugins/home_assistant/socket"
var ConfigFile = TEST_PREFIX + "/configs/plugins/home_assistant/config.json"
var APITokenFile = TEST_PREFIX + "/configs/plugins/home_assistant/api-token"
var DevicesPublicConfigFile = TEST_PREFIX + "/state/public/devices-public.json"

type Config struct {
	// APIToken authenticates ha_sync against the SPR API on localhost.
	// Normally left empty: the read-only install token SPR writes to
	// InstallTokenPath (api-token file) is used. Set to override.
	APIToken string

	// RouterID is a stable unique id generated on first startup. Home
	// Assistant uses it as the config entry unique_id.
	RouterID string

	// PollIntervalSeconds between SPR state refreshes (default 10).
	PollIntervalSeconds int
}

var gConfig = Config{PollIntervalSeconds: 10}
var ConfigMtx sync.RWMutex

func genToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// loadConfig reads config.json, fills defaults, and persists anything
// newly generated.
func loadConfig() {
	ConfigMtx.Lock()
	defer ConfigMtx.Unlock()

	data, err := os.ReadFile(ConfigFile)
	if err == nil {
		_ = json.Unmarshal(data, &gConfig)
	}

	dirty := false
	if gConfig.PollIntervalSeconds <= 0 {
		gConfig.PollIntervalSeconds = 10
		dirty = true
	}
	if gConfig.RouterID == "" {
		gConfig.RouterID = genToken()[:16]
		dirty = true
	}
	if dirty {
		saveConfigLocked()
	}
}

func saveConfigLocked() {
	_ = os.MkdirAll(filepath.Dir(ConfigFile), 0o700)
	data, err := json.MarshalIndent(gConfig, "", "  ")
	if err != nil {
		return
	}
	tmp := ConfigFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, ConfigFile)
}

func configCopy() Config {
	ConfigMtx.RLock()
	defer ConfigMtx.RUnlock()
	return gConfig
}
