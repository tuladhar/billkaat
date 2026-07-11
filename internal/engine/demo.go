package engine

import (
	"github.com/billkaat/billkaat/internal/checks"
	"github.com/billkaat/billkaat/internal/store"
)

// SeedDemo inserts one realistic completed scan so the UI can be explored
// (and screenshotted for the landing page) without AWS credentials.
// Run with: billkaat -demo
func SeedDemo(st *store.Store) (int64, error) {
	id, err := st.CreateScan("ap-south-1")
	if err != nil {
		return 0, err
	}
	_ = st.SetScanAccount(id, "123456789012")

	f := func(check, res, rtype string, sev checks.Severity, title, detail, rec string, save float64) checks.Finding {
		return checks.Finding{CheckID: check, ResourceID: res, ResourceType: rtype,
			Region: "ap-south-1", Severity: sev, Title: title, Detail: detail,
			Recommendation: rec, MonthlySavingsUSD: save}
	}

	findings := []checks.Finding{
		f("ebs-unattached", "vol-0a1b2c3d4e5f6a7b8", "EBS Volume", checks.SevHigh,
			"100 GB gp2 volume attached to nothing",
			"Volume has status 'available' (created 2025-11-02). You pay for it every hour even though no instance uses it.",
			"Snapshot it if you might need the data, then delete the volume.", 10.00),
		f("ebs-unattached", "vol-0c9d8e7f6a5b4c3d2", "EBS Volume", checks.SevHigh,
			"160 GB gp3 volume attached to nothing",
			"Volume has status 'available' (created 2026-01-14).",
			"Snapshot it if you might need the data, then delete the volume.", 12.80),
		f("ebs-gp2-to-gp3", "vol-0f1e2d3c4b5a69788", "EBS Volume", checks.SevLow,
			"500 GB gp2 volume can migrate to gp3",
			"gp3 is ~20% cheaper per GB and gives 3,000 IOPS baseline.",
			"Modify the volume type to gp3 — it's an online operation with no downtime.", 10.00),
		f("eip-unassociated", "13.234.51.102", "Elastic IP", checks.SevMedium,
			"Elastic IP allocated but not attached to anything",
			"Idle public IPv4 addresses are billed at $0.005/hour.",
			"Release the address, or attach it to the instance that needs it.", 3.65),
		f("eip-unassociated", "65.1.92.240", "Elastic IP", checks.SevMedium,
			"Elastic IP allocated but not attached to anything",
			"Idle public IPv4 addresses are billed at $0.005/hour.",
			"Release the address, or attach it to the instance that needs it.", 3.65),
		f("ec2-stopped-storage", "i-0123456789abcdef0 (staging-api)", "EC2 Instance", checks.SevMedium,
			"Stopped instance still paying $16.00/mo for its disks",
			"Instance has been stopped but its 200 GB of EBS keeps billing. Snapshots of the same data would cost about $10.00/mo.",
			"If it's staying stopped, snapshot the volumes and terminate. If it runs on a schedule, automate stop/start.", 6.00),
		f("snapshot-stale", "snap-0aa11bb22cc33dd44", "EBS Snapshot", checks.SevLow,
			"120 GB snapshot untouched for 14 months",
			"Standard-tier snapshots cost about $0.05 per GB-month. Because snapshots are incremental this is an upper-bound estimate.",
			"Delete it if the source volume is gone or newer snapshots exist, or archive it.", 6.00),
		f("elb-no-targets", "staging-alb", "Application Load Balancer", checks.SevHigh,
			"Load balancer has zero registered targets",
			"An ALB bills ~$16.43/mo just for existing. This one forwards traffic to nothing.",
			"Delete the load balancer, or register the target group it was meant to serve.", 16.43),
		f("sg-open-to-world", "sg-0a1b2c3d (web-servers)", "Security Group", checks.SevHigh,
			"SSH (22) open to the entire internet",
			"Inbound rule allows 0.0.0.0/0 on a sensitive port.",
			"Restrict to your office/VPN CIDR, or move SSH behind SSM Session Manager.", 0),
		f("sg-open-to-world", "sg-09z8y7x6 (default)", "Security Group", checks.SevCritical,
			"ALL traffic open to the entire internet",
			"Inbound rule allows every port and protocol from 0.0.0.0/0.",
			"Remove the allow-all rule and open only the specific ports each service needs.", 0),
	}
	if err := st.AddFindings(id, findings); err != nil {
		return 0, err
	}

	// Per-check statuses: demo values for free checks, locked for pro.
	type row struct {
		id, status string
		n          int
		save       float64
	}
	rows := []row{
		{"ebs-unattached", "flagged", 2, 22.80},
		{"ebs-gp2-to-gp3", "flagged", 1, 10.00},
		{"eip-unassociated", "flagged", 2, 7.30},
		{"ec2-stopped-storage", "flagged", 1, 6.00},
		{"snapshot-stale", "flagged", 1, 6.00},
		{"elb-no-targets", "flagged", 1, 16.43},
		{"elb-all-unhealthy", "passed", 0, 0},
		{"sg-open-to-world", "flagged", 2, 0},
	}
	var total float64
	var count int
	for _, r := range rows {
		_ = st.SetCheckStatus(id, r.id, r.status, "", r.n, r.save, 850)
		total += r.save
		count += r.n
	}
	for _, c := range checks.All() {
		if c.Meta().Locked {
			_ = st.SetCheckStatus(id, c.Meta().ID, "locked", "", 0, 0, 0)
		}
	}
	return id, st.FinishScan(id, total, count)
}
