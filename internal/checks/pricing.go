package checks

// Approximate on-demand prices in USD (us-east-1 rates). Real prices vary a
// little by region; these are deliberately simple estimates whose job is to
// size the opportunity, not to reproduce the bill to the cent. Update this
// table occasionally, or replace with the AWS Pricing API in the Pro build.
const (
	// $0.005/hr for a public IPv4 address that is allocated but idle.
	PriceEIPMonthly = 3.65
	// $0.0225/hr base rate for ALB/NLB, excludes LCU usage.
	PriceALBMonthly = 16.43
	PriceNLBMonthly = 16.43
	// $0.045/hr per NAT gateway, excludes per-GB data processing.
	PriceNATMonthly = 32.85
	// EBS snapshot standard tier, per GB-month. Snapshots are incremental,
	// so treat estimates based on volume size as an upper bound.
	PriceSnapshotPerGBMonth = 0.05
)

var ebsPerGBMonth = map[string]float64{
	"gp2":      0.10,
	"gp3":      0.08,
	"io1":      0.125,
	"io2":      0.125,
	"st1":      0.045,
	"sc1":      0.015,
	"standard": 0.05,
}

// EBSPricePerGBMonth returns the approximate monthly per-GB price for a
// volume type, defaulting to gp2 pricing for unknown types.
func EBSPricePerGBMonth(volType string) float64 {
	if p, ok := ebsPerGBMonth[volType]; ok {
		return p
	}
	return 0.10
}
