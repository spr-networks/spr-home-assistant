package main

// The plugin serves a read-only API on its unix socket only. SPR reverse-
// proxies it at /plugins/home_assistant/ behind SPR's own authentication, so
// Home Assistant reaches it with an SPR token scoped to that path (":r", so
// GET only). There is no TCP listener and no auth in this process: SPR has
// already authenticated and authorized the caller, and it strips credentials
// before proxying. Nothing here mutates SPR state; the one side-effecting
// route, /ha/v1/wake, emits a Wake-on-LAN packet onto the LAN, no API touch.

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// ---- SSE hub: pushes device connect/disconnect to HA over a GET stream ----

type sseHub struct {
	mtx  sync.Mutex
	subs map[chan []byte]struct{}
}

var gHub = &sseHub{subs: map[chan []byte]struct{}{}}

func (h *sseHub) subscribe() chan []byte {
	ch := make(chan []byte, 16)
	h.mtx.Lock()
	h.subs[ch] = struct{}{}
	h.mtx.Unlock()
	return ch
}

func (h *sseHub) unsubscribe(ch chan []byte) {
	h.mtx.Lock()
	delete(h.subs, ch)
	h.mtx.Unlock()
}

func (h *sseHub) broadcast(event string, payload interface{}) {
	data, err := json.Marshal(map[string]interface{}{"event": event, "data": payload})
	if err != nil {
		return
	}
	h.mtx.Lock()
	for ch := range h.subs {
		select {
		case ch <- data:
		default: // slow consumer: drop rather than block the bus
		}
	}
	h.mtx.Unlock()
}

// ---- handlers (all read-only) ----

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleProbe identifies the router so the HA config flow can set a stable
// unique id and title. No device data here.
func handleProbe(w http.ResponseWriter, r *http.Request) {
	snap := gState.get()
	writeJSON(w, map[string]interface{}{
		"product":  "spr",
		"api":      1,
		"id":       configCopy().RouterID,
		"hostname": snap.Router.Hostname,
		"version":  snap.Router.Version,
	})
}

func handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, gState.get())
}

// handleStaticDiscovery serves the unauthenticated identify document. SPR
// exposes it at /admin/custom_plugin/home_assistant/static/discovery.json
// (public router, no auth) because it lives under /static/. It carries only
// non-secret identity: the opaque router id, product, and display name — no
// version, no device data, no capability. Home Assistant's config flow reads
// it to present the router; the token still gates everything under /ha/v1.
func handleStaticDiscovery(w http.ResponseWriter, r *http.Request) {
	snap := gState.get()
	name := snap.Router.Hostname
	if name == "" {
		name = "SPR"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]interface{}{
		"product": "spr",
		"api":     1,
		"id":      configCopy().RouterID,
		"name":    name,
	})
}

// handleIndex serves a minimal static page. HasUI is enabled only so SPR
// registers the public /static/ route the discovery document needs; this
// keeps the plugin's UI slot from 404-ing.
func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>SPR Home Assistant</title>`+
		`<body style="font-family:system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem">`+
		`<h1>SPR · Home Assistant</h1>`+
		`<p>This plugin exposes read-only router state to Home Assistant. It has no `+
		`interactive UI. Add it in Home Assistant with a read-only SPR API token `+
		`scoped to <code>/plugins/home_assistant/:r</code>.</p>`+
		`<p><a href="https://github.com/spr-networks/home_assistant_integration">Documentation</a></p>`)
}

func handleDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, gState.get().Devices)
}

// handleWake sends a Wake-on-LAN magic packet. Deliberately a GET so it
// works through SPR's ":r" (GET-only) token scope: it reads nothing but the
// device table and writes only a UDP packet to the LAN — never to SPR.
func handleWake(w http.ResponseWriter, r *http.Request) {
	mac := normalizeMAC(r.URL.Query().Get("mac"))
	if !isMAC(mac) {
		http.Error(w, "invalid mac", http.StatusBadRequest)
		return
	}
	ip := ""
	for _, d := range gState.get().Devices {
		if d.MAC == mac {
			ip = d.IP
			break
		}
	}
	if err := sendWOL(mac, ip); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println("wol: magic packet sent for", mac)
	writeJSON(w, map[string]bool{"ok": true})
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := gHub.subscribe()
	defer gHub.unsubscribe(ch)

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

// newRouter builds the read-only route table served over the unix socket.
func newRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)
	// Authenticated (SPR proxies these at /plugins/home_assistant/, gated by
	// the caller's scoped token). Read-only; wake writes only to the LAN.
	router.HandleFunc("/ha/v1/probe", handleProbe).Methods("GET")
	router.HandleFunc("/ha/v1/state", handleState).Methods("GET")
	router.HandleFunc("/ha/v1/devices", handleDevices).Methods("GET")
	router.HandleFunc("/ha/v1/wake", handleWake).Methods("GET")
	router.HandleFunc("/ha/v1/events", handleEvents).Methods("GET")

	// Unauthenticated static content. SPR only ever routes /static/* here via
	// its public proxy (it hardcodes the /static/ prefix), so nothing under
	// /ha/v1 is reachable without auth. Discovery data only — no secrets.
	router.HandleFunc("/static/discovery.json", handleStaticDiscovery).Methods("GET")
	router.HandleFunc("/index.html", handleIndex).Methods("GET")
	return router
}

// startUnixServer serves the API on the plugin's unix socket. This is the
// only listener the plugin opens.
func startUnixServer() {
	_ = os.MkdirAll(filepath.Dir(UNIX_PLUGIN_LISTENER), 0o755)
	_ = os.Remove(UNIX_PLUGIN_LISTENER)
	listener, err := net.Listen("unix", UNIX_PLUGIN_LISTENER)
	if err != nil {
		log.Fatal("unix listener:", err)
	}
	// only the SPR API proxy (root in the api container) needs to connect
	_ = os.Chmod(UNIX_PLUGIN_LISTENER, 0o660)

	server := http.Server{
		Handler:           http.MaxBytesHandler(newRouter(), 64*1024),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Println("read-only API listening on", UNIX_PLUGIN_LISTENER)
	log.Fatal(server.Serve(listener))
}
