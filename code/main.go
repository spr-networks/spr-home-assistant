package main

import (
	"log"
)

func main() {
	log.Println("spr home assistant sync starting (read-only)")
	loadConfig()
	initSPRAPIBase()

	go pollLoop()
	go sprbusListener()
	go advertiseMDNS()

	// The unix-socket API is the only HTTP listener; SPR proxies it to HA.
	startUnixServer()
}
