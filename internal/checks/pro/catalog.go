// Package pro holds the paid check set.
//
// How the open-core split works:
//   - This file (the catalog) has no build tag. Every build — including the
//     public Community build — knows the *names and descriptions* of the pro
//     checks, so the UI can show them as locked rows. That is the upsell.
//   - locked_stub.go (build tag !pro) registers each catalog entry as a
//     locked placeholder.
//   - Files with the `pro` build tag contain the real implementations and
//     live only in your PRIVATE repository. `go build -tags pro` produces
//     the Pro binary you sell. nat_idle.go is included here as a worked
//     example of the pattern — move it (and its siblings) to the private
//     overlay before you publish this repo.
package pro

import "github.com/billkaat/billkaat/internal/checks"

// Catalog lists every Pro check. Add a row here and the Community build
// automatically advertises it as locked.
var Catalog = []checks.Meta{
	{ID: "ec2-rightsizing", Name: "EC2 rightsizing", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Analyzes 14 days of CloudWatch CPU and network data to find over-provisioned instances and suggests cheaper types."},
	{ID: "nat-idle", Name: "Idle NAT gateways", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "NAT gateways cost ~$33/mo each even when nothing uses them. Flags gateways with near-zero traffic over 14 days."},
	{ID: "rds-idle", Name: "Idle RDS instances", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Databases with near-zero connections over 14 days that are still billing every hour."},
	{ID: "rds-rightsizing", Name: "RDS rightsizing", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Over-provisioned database instances based on CPU, memory, and connection headroom."},
	{ID: "ebs-iops-overprovisioned", Name: "Over-provisioned EBS IOPS", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "io1/io2/gp3 volumes paying for provisioned IOPS they never consume."},
	{ID: "logs-retention", Name: "CloudWatch Logs without retention", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Log groups set to 'never expire' that grow — and bill — forever."},
	{ID: "dynamodb-capacity", Name: "DynamoDB capacity mode", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Provisioned tables that would be cheaper on-demand, and vice versa."},
	{ID: "lambda-memory", Name: "Lambda memory rightsizing", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Functions allocated far more memory than they use."},
	{ID: "s3-lifecycle", Name: "S3 lifecycle & storage class", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Buckets with no lifecycle rules and objects sitting in Standard that belong in IA or Glacier."},
	{ID: "ri-sp-coverage", Name: "Reserved Instance / Savings Plan gaps", Category: checks.CatCost, Tier: checks.TierPro,
		Description: "Steady-state on-demand usage that a commitment would cut by 30-60%."},
	{ID: "iam-hygiene", Name: "IAM hygiene", Category: checks.CatSecurity, Tier: checks.TierPro,
		Description: "Access keys older than 90 days, users without MFA, and recent root account usage."},
	{ID: "public-exposure", Name: "Public exposure audit", Category: checks.CatSecurity, Tier: checks.TierPro,
		Description: "Public S3 buckets, publicly accessible RDS instances, and public AMIs or snapshots."},
}

// locked is the placeholder registered for a pro check that is not compiled
// into this binary. The engine never runs locked checks.
type locked struct{ m checks.Meta }

func (l locked) Meta() checks.Meta {
	m := l.m
	m.Locked = true
	return m
}

func (l locked) Run(*checks.RunContext) ([]checks.Finding, error) {
	return nil, checks.ErrLocked
}

// metaFor lets a real implementation reuse its catalog entry.
func metaFor(id string) checks.Meta {
	for _, m := range Catalog {
		if m.ID == id {
			return m
		}
	}
	return checks.Meta{ID: id, Name: id, Tier: checks.TierPro}
}
