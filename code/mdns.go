package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

const MDNSServiceType = "_spr-ha._tcp"

// advertiseMDNS registers the HA-facing API over mDNS so Home Assistant's
// zeroconf discovery can find the router without manual configuration.
// Re-registers periodically so a restarted mdns cache still finds us.
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

	for {
		server, err := zeroconf.Register(instance, MDNSServiceType, "local.", config.ListenPort, txt, nil)
		if err != nil {
			log.Println("mdns register failed:", err)
			time.Sleep(time.Minute)
			continue
		}
		// zeroconf answers queries as long as the server is alive; refresh
		// registration every hour to survive interface changes.
		time.Sleep(time.Hour)
		server.Shutdown()
	}
}
