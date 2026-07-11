//go:build pro

package pro

import "github.com/billkaat/billkaat/internal/checks"

// implemented lists catalog IDs that have a real implementation compiled in.
// Each implementation file registers itself; everything else is registered
// as a locked placeholder until it ships in a Pro update ("free updates").
var implemented = map[string]bool{
	"nat-idle": true,
}

func init() {
	for _, m := range Catalog {
		if implemented[m.ID] {
			continue
		}
		checks.Register(locked{m})
	}
}
