package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

// The plugin advertises a DNS-SD service so Home Assistant's zeroconf browser
// surfaces the router as a discoverable integration. The advertisement is a
// bare trigger: it carries no capability and no secret. All identify data is
// served separately from the unauthenticated static path (discovery.json),
// and everything sensitive stays behind SPR's auth on the /ha/v1 routes.
//
// SRV points at the SPR API port (443) — that's where Home Assistant reaches
// the router, not this plugin. mDNS on the box coexists with SPR's
// multicast_udp_proxy: the responder uses SO_REUSEPORT to share UDP 5353.
const (
	MDNSServiceType = "_spr-ha._tcp"
	MDNSServicePort = 443
)

// lanInterfaces returns multicast-capable interfaces excluding loopback,
// docker bridges, and the WAN uplink: discovery should only face the LAN.
func lanInterfaces(wanIface string) []net.Interface {
	all, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, iface := range all {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 || iface.Name == wanIface {
			continue
		}
		out = append(out, iface)
	}
	return out
}

// advertiseMDNS registers the discovery beacon and re-registers hourly so
// interface changes are picked up.
func advertiseMDNS() {
	if configCopy().MDNSDisabled {
		return
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "spr"
	}
	instance := fmt.Sprintf("SPR (%s)", hostname)

	// No secrets in TXT: identify data comes from the static discovery path.
	txt := []string{"product=spr"}

	// give the first poll a moment to identify the WAN uplink
	for i := 0; i < 10 && gState.get().Router.WANIface == ""; i++ {
		time.Sleep(time.Second)
	}

	for {
		ifaces := lanInterfaces(gState.get().Router.WANIface)
		server, err := zeroconf.Register(instance, MDNSServiceType, "local.", MDNSServicePort, txt, ifaces)
		if err != nil {
			log.Println("mdns register failed:", err)
			time.Sleep(time.Minute)
			continue
		}
		time.Sleep(time.Hour)
		server.Shutdown()
	}
}
