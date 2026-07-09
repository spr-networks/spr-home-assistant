package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// ---- SSE hub: pushes device connect/disconnect + state changes to HA ----

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

// ---- auth ----

// lanOnly rejects requests that don't originate from a private/local
// address. The listener binds all interfaces under host networking, and
// while SPR's firewall drops unsolicited WAN input, a router API should not
// depend on a single layer for that.
func lanOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authOK(r *http.Request) bool {
	token := configCopy().HAToken
	if token == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(token)) == 1
}

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authOK(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- handlers ----

// probeInfo is served unauthenticated so the HA config flow can identify the
// router during zeroconf discovery. No device data is exposed here.
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

func handleDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, gState.get().Devices)
}

type blockRequest struct {
	Blocked bool `json:"blocked"`
}

func handleBlockDevice(w http.ResponseWriter, r *http.Request) {
	mac := normalizeMAC(mux.Vars(r)["mac"])
	var req blockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := sprSetDeviceBlocked(mac, req.Blocked); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	requestRefresh()
	writeJSON(w, map[string]bool{"blocked": req.Blocked})
}

type guestWifiRequest struct {
	Enabled bool `json:"enabled"`
}

func handleGuestWifi(w http.ResponseWriter, r *http.Request) {
	var req guestWifiRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := sprSetGuestWifi(req.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	requestRefresh()
	writeJSON(w, map[string]bool{"enabled": req.Enabled})
}

func handleReboot(w http.ResponseWriter, r *http.Request) {
	if err := sprRestart(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

type wolRequest struct {
	MAC string `json:"mac"`
}

func handleWOL(w http.ResponseWriter, r *http.Request) {
	var req wolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ip := ""
	for _, d := range gState.get().Devices {
		if d.MAC == normalizeMAC(req.MAC) {
			ip = d.IP
			break
		}
	}
	if err := sendWOL(req.MAC, ip); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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

// startHAServer serves the Home Assistant-facing API on the LAN.
func startHAServer() {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/api/probe", handleProbe).Methods("GET")
	router.HandleFunc("/api/state", requireAuth(handleState)).Methods("GET")
	router.HandleFunc("/api/devices", requireAuth(handleDevices)).Methods("GET")
	router.HandleFunc("/api/device/{mac}/block", requireAuth(handleBlockDevice)).Methods("PUT")
	router.HandleFunc("/api/guest_wifi", requireAuth(handleGuestWifi)).Methods("PUT")
	router.HandleFunc("/api/system/restart", requireAuth(handleReboot)).Methods("POST")
	router.HandleFunc("/api/wol", requireAuth(handleWOL)).Methods("POST")
	router.HandleFunc("/api/events", requireAuth(handleEvents)).Methods("GET")

	addr := fmt.Sprintf(":%d", configCopy().ListenPort)
	server := &http.Server{
		Addr:              addr,
		Handler:           lanOnly(http.MaxBytesHandler(router, 64*1024)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Println("HA API listening on", addr)
	log.Fatal(server.ListenAndServe())
}
