package main

// vesttVersion is the application version, injected at build time via:
//
//	go build -ldflags "-X main.vesttVersion=<version>"
//
// Defaults to "dev" for local builds. All API responses exposing app_version
// read this value.
var vesttVersion = "dev"
