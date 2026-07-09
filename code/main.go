package main

import (
	"log"
)

func main() {
	log.Println("spr home assistant sync starting")
	loadConfig()

	go pollLoop()
	go sprbusListener()
	go advertiseMDNS()
	go startHAServer()

	// Blocks; the unix socket API is what SPR health-checks.
	startPluginServer()
}
