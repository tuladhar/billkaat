// Package checks defines the check interface, the finding model and the
// registry that both the free and pro check sets plug into.
package checks

import (
	"context"
	"errors"
	"sort"

	"github.com/billkaat/billkaat/internal/awsx"
)

// Tier separates the open-source check set from the paid one.
type Tier string

const (
	TierFree Tier = "free"
	TierPro  Tier = "pro"
)

// Category describes what kind of problem a check looks for.
type Category string

const (
	CatCost        Category = "cost"
	CatSecurity    Category = "security"
	CatPerformance Category = "performance"
)

// Severity of an individual finding.
type Severity string

const (
	SevInfo     Severity = "info"
	SevLow      Severity = "low"
	SevMedium   Severity = "medium"
	SevHigh     Severity = "high"
	SevCritical Severity = "critical"
)

// Meta is the static description of a check, shown in the UI even when the
// check itself is locked.
type Meta struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    Category `json:"category"`
	Tier        Tier     `json:"tier"`
	Locked      bool     `json:"locked"`
}

// Finding is a single flagged resource.
type Finding struct {
	CheckID           string   `json:"check_id"`
	ResourceID        string   `json:"resource_id"`
	ResourceType      string   `json:"resource_type"`
	Region            string   `json:"region"`
	Severity          Severity `json:"severity"`
	Title             string   `json:"title"`
	Detail            string   `json:"detail"`
	Recommendation    string   `json:"recommendation"`
	MonthlySavingsUSD float64  `json:"monthly_savings_usd"`
}

// RunContext is handed to every check.
type RunContext struct {
	Ctx    context.Context
	AWS    *awsx.Clients
	Region string
}

// Check is the one interface every check implements. Adding a check to the
// product is: write a struct, implement Meta and Run, call Register in init.
type Check interface {
	Meta() Meta
	Run(rc *RunContext) ([]Finding, error)
}

// ErrLocked is returned by locked placeholder checks. The engine never runs
// locked checks, so this is a backstop.
var ErrLocked = errors.New("this check requires a Pro license and the Pro build")

var registry []Check

// Register adds a check to the global registry. Call from init().
func Register(c Check) { registry = append(registry, c) }

// All returns every registered check, free checks first, stable order.
func All() []Check {
	out := make([]Check, len(registry))
	copy(out, registry)
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].Meta(), out[j].Meta()
		if mi.Tier != mj.Tier {
			return mi.Tier == TierFree
		}
		return mi.ID < mj.ID
	})
	return out
}
