package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const MDNSServiceType = "_spr-ha._tcp"

// lanInterfaces returns multicast-capable interfaces excluding loopback,
// docker bridges, and the WAN uplink: the guest-visible advertisement should
// only face the LAN.
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

// advertiseMDNS registers the HA-facing API over mDNS so Home Assistant's
// zeroconf discovery can find the router without manual configuration.
// Re-registers hourly so interface changes are picked up.
func advertiseMDNS() {
	config := configCopy()
	if config.MDNSDisabled {
		return
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "spr"
	}
	instance := fmt.Sprintf("SPR (%s)", hostname)

	txt := []string{
		"api=1",
		"product=spr",
		"id=" + config.RouterID,
	}

	// give the first poll a moment to identify the WAN uplink
	for i := 0; i < 10 && gState.get().Router.WANIface == ""; i++ {
		time.Sleep(time.Second)
	}

	for {
		ifaces := lanInterfaces(gState.get().Router.WANIface)
		server, err := zeroconf.Register(instance, MDNSServiceType, "local.", config.ListenPort, txt, ifaces)
		if err != nil {
			log.Println("mdns register failed:", err)
			time.Sleep(time.Minute)
			continue
		}
		time.Sleep(time.Hour)
		server.Shutdown()
	}
}
