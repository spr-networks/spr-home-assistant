package main

import (
	"encoding/json"
	"log"
	"time"

	sprbus "github.com/spr-networks/sprbus-json"
)

// wifi/dhcp sprbus payloads all carry the station MAC in "MAC"
type busDeviceEvent struct {
	MAC   string
	Iface string
	Name  string
}

// sprbusListener keeps presence fresh between polls: any wifi auth, station
// disconnect, or DHCP activity triggers an immediate state refresh, so HA
// sees connects/disconnects within about a second.
func sprbusListener() {
	topics := []string{
		"wifi:auth:success",
		"wifi:station:disconnect",
		"dhcp:request",
		"device:",
	}
	for _, topic := range topics {
		go subscribeForever(topic)
	}
}

func subscribeForever(topic string) {
	for {
		err := sprbus.HandleEvent(topic, handleBusEvent)
		log.Println("sprbus subscription lost for", topic, ":", err)
		time.Sleep(5 * time.Second)
	}
}

func handleBusEvent(topic string, value string) {
	var event busDeviceEvent
	_ = json.Unmarshal([]byte(value), &event)

	if event.MAC != "" && isMAC(event.MAC) {
		switch topic {
		case "wifi:auth:success", "dhcp:request":
			gState.markSeen(event.MAC, time.Now())
		}
	}

	requestRefresh()
}
