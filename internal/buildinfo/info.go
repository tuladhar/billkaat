// Package buildinfo carries edition and version constants stamped at build time.
package buildinfo

// Version is overridden at build time:
//
//	go build -ldflags "-X github.com/billkaat/billkaat/internal/buildinfo.Version=v0.2.0"
var Version = "0.1.0-dev"
