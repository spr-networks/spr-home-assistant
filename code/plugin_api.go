package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/mux"
)

// The unix socket API is reachable through the SPR API proxy at
// /plugins/home_assistant/ and is what the SPR UI (admin) uses: it exposes
// the pairing token and settings, which the LAN API never does.

type pluginStatus struct {
	Config
	Paired     bool  `json:"Paired"`
	Devices    int   `json:"Devices"`
	Connected  int   `json:"Connected"`
	LastUpdate int64 `json:"LastUpdate"`
}

func pluginGetConfig(w http.ResponseWriter, r *http.Request) {
	snap := gState.get()
	config := configCopy()
	status := pluginStatus{
		Config:     config,
		Devices:    len(snap.Devices),
		Connected:  snap.Router.ClientsConnected,
		LastUpdate: snap.Timestamp,
	}
	status.APIToken = "*masked*"
	writeJSON(w, status)
}

func pluginSetConfig(w http.ResponseWriter, r *http.Request) {
	var next Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if next.ListenPort < 0 || next.ListenPort > 65535 {
		http.Error(w, "invalid ListenPort", http.StatusBadRequest)
		return
	}
	ConfigMtx.Lock()
	if next.ListenPort > 0 {
		gConfig.ListenPort = next.ListenPort
	}
	if next.PollIntervalSeconds > 0 {
		gConfig.PollIntervalSeconds = next.PollIntervalSeconds
	}
	gConfig.MDNSDisabled = next.MDNSDisabled
	saveConfigLocked()
	ConfigMtx.Unlock()
	pluginGetConfig(w, r)
}

// pluginRotateToken invalidates the current HA pairing token.
func pluginRotateToken(w http.ResponseWriter, r *http.Request) {
	ConfigMtx.Lock()
	gConfig.HAToken = genToken()
	saveConfigLocked()
	token := gConfig.HAToken
	ConfigMtx.Unlock()
	writeJSON(w, map[string]string{"HAToken": token})
}

func startPluginServer() {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/config", pluginGetConfig).Methods("GET")
	router.HandleFunc("/config", pluginSetConfig).Methods("PUT")
	router.HandleFunc("/token/rotate", pluginRotateToken).Methods("PUT")

	_ = os.MkdirAll(filepath.Dir(UNIX_PLUGIN_LISTENER), 0o755)
	_ = os.Remove(UNIX_PLUGIN_LISTENER)
	listener, err := net.Listen("unix", UNIX_PLUGIN_LISTENER)
	if err != nil {
		log.Fatal("plugin listener:", err)
	}
	// only the SPR API proxy (root in the api container) needs to connect
	_ = os.Chmod(UNIX_PLUGIN_LISTENER, 0o660)
	server := http.Server{
		Handler:           http.MaxBytesHandler(router, 64*1024),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Println("plugin API listening on", UNIX_PLUGIN_LISTENER)
	log.Fatal(server.Serve(listener))
}
