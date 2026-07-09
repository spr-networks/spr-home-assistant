package main

import (
	"fmt"
	"net"
	"strings"
)

// parseMAC accepts aa:bb:cc:dd:ee:ff, aa-bb-..., or bare hex.
func parseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(s)
	if !strings.ContainsAny(s, ":-.") && len(s) == 12 {
		var parts []string
		for i := 0; i < 12; i += 2 {
			parts = append(parts, s[i:i+2])
		}
		s = strings.Join(parts, ":")
	}
	hw, err := net.ParseMAC(s)
	if err != nil {
		return nil, err
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("wol: need a 48-bit MAC, got %s", hw)
	}
	return hw, nil
}

// sendWOL broadcasts a magic packet for mac, and also unicasts to the
// device's last known IP (helps when broadcast doesn't cross a VLAN).
// This writes to the LAN, not to any SPR API — the plugin's SPR token
// stays read-only.
func sendWOL(mac string, ip string) error {
	hw, err := parseMAC(mac)
	if err != nil {
		return err
	}

	payload := make([]byte, 0, 102)
	payload = append(payload, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	for i := 0; i < 16; i++ {
		payload = append(payload, hw...)
	}

	targets := []string{"255.255.255.255:9"}
	if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
		targets = append(targets, parsed.String()+":9")
	}

	var lastErr error
	sent := false
	for _, target := range targets {
		conn, err := net.Dial("udp4", target)
		if err != nil {
			lastErr = err
			continue
		}
		_, err = conn.Write(payload)
		conn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		sent = true
	}
	if !sent {
		return lastErr
	}
	return nil
}
