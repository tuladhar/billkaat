//go:build !pro

package pro

import "github.com/billkaat/billkaat/internal/checks"

// Community build: every pro check is registered as a locked placeholder so
// the UI can advertise what the Pro build finds.
func init() {
	for _, m := range Catalog {
		checks.Register(locked{m})
	}
}
