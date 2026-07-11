package free

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

// ---- stale snapshots ----

const (
	staleSnapshotDays = 180
	maxSnapshotRows   = 100 // individual findings before we aggregate the tail
)

type snapshotStale struct{}

func init() { checks.Register(snapshotStale{}) }

func (snapshotStale) Meta() checks.Meta {
	return checks.Meta{
		ID:          "snapshot-stale",
		Name:        "Stale EBS snapshots",
		Description: fmt.Sprintf("Snapshots owned by this account that are older than %d days.", staleSnapshotDays),
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (snapshotStale) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	cutoff := time.Now().AddDate(0, 0, -staleSnapshotDays)
	var stale []ec2types.Snapshot

	p := ec2.NewDescribeSnapshotsPaginator(rc.AWS.EC2, &ec2.DescribeSnapshotsInput{
		OwnerIds: []string{"self"},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.Snapshots {
			if aws.ToTime(s.StartTime).Before(cutoff) {
				stale = append(stale, s)
			}
		}
	}

	var out []checks.Finding
	var tailGB int64
	for i, s := range stale {
		size := int64(aws.ToInt32(s.VolumeSize))
		if i >= maxSnapshotRows {
			tailGB += size
			continue
		}
		age := int(time.Since(aws.ToTime(s.StartTime)).Hours() / 24)
		out = append(out, checks.Finding{
			ResourceID:   aws.ToString(s.SnapshotId),
			ResourceType: "EBS Snapshot",
			Severity:     checks.SevLow,
			Title:        fmt.Sprintf("%d GB snapshot untouched for %d days", size, age),
			Detail: "Standard-tier snapshots cost about $0.05 per GB-month. Because snapshots " +
				"are incremental, this estimate is an upper bound.",
			Recommendation:    "Delete it if the source volume is gone or newer snapshots exist, or move it to the archive tier.",
			MonthlySavingsUSD: float64(size) * checks.PriceSnapshotPerGBMonth,
		})
	}
	if extra := len(stale) - maxSnapshotRows; extra > 0 {
		out = append(out, checks.Finding{
			ResourceID:   fmt.Sprintf("%d more snapshots", extra),
			ResourceType: "EBS Snapshot",
			Severity:     checks.SevLow,
			Title:        fmt.Sprintf("…and %d more stale snapshots totalling %d GB", extra, tailGB),
			Detail:       fmt.Sprintf("Only the first %d are listed individually. Export the CSV or clean up in batches.", maxSnapshotRows),
			Recommendation:    "Set a snapshot lifecycle policy (Data Lifecycle Manager) so this stops accumulating.",
			MonthlySavingsUSD: float64(tailGB) * checks.PriceSnapshotPerGBMonth,
		})
	}
	return out, nil
}

// ---- stopped instances still paying for storage ----

type ec2StoppedStorage struct{}

func init() { checks.Register(ec2StoppedStorage{}) }

func (ec2StoppedStorage) Meta() checks.Meta {
	return checks.Meta{
		ID:          "ec2-stopped-storage",
		Name:        "Stopped instances still paying for disks",
		Description: "A stopped instance costs nothing for compute, but its EBS volumes keep billing at full price.",
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (ec2StoppedStorage) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	type inst struct {
		id, name string
		volIDs   []string
	}
	var instances []inst
	var allVolIDs []string

	p := ec2.NewDescribeInstancesPaginator(rc.AWS.EC2, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"stopped"}},
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Reservations {
			for _, i := range r.Instances {
				in := inst{id: aws.ToString(i.InstanceId), name: nameTag(i.Tags)}
				for _, bdm := range i.BlockDeviceMappings {
					if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
						in.volIDs = append(in.volIDs, *bdm.Ebs.VolumeId)
						allVolIDs = append(allVolIDs, *bdm.Ebs.VolumeId)
					}
				}
				instances = append(instances, in)
			}
		}
	}
	if len(instances) == 0 {
		return nil, nil
	}

	// Resolve volume sizes/types in chunks of 100 IDs per call.
	volCost := map[string]float64{}
	volSize := map[string]int64{}
	for start := 0; start < len(allVolIDs); start += 100 {
		end := min(start+100, len(allVolIDs))
		resp, err := rc.AWS.EC2.DescribeVolumes(rc.Ctx, &ec2.DescribeVolumesInput{
			VolumeIds: allVolIDs[start:end],
		})
		if err != nil {
			return nil, err
		}
		for _, v := range resp.Volumes {
			size := int64(aws.ToInt32(v.Size))
			volSize[aws.ToString(v.VolumeId)] = size
			volCost[aws.ToString(v.VolumeId)] = float64(size) * checks.EBSPricePerGBMonth(string(v.VolumeType))
		}
	}

	var out []checks.Finding
	for _, in := range instances {
		var gb int64
		var ebsMonthly float64
		for _, id := range in.volIDs {
			gb += volSize[id]
			ebsMonthly += volCost[id]
		}
		if gb == 0 {
			continue
		}
		snapshotAlt := float64(gb) * checks.PriceSnapshotPerGBMonth
		saving := ebsMonthly - snapshotAlt
		if saving < 0 {
			saving = 0
		}
		label := in.id
		if in.name != "" {
			label = fmt.Sprintf("%s (%s)", in.id, in.name)
		}
		out = append(out, checks.Finding{
			ResourceID:   label,
			ResourceType: "EC2 Instance",
			Severity:     checks.SevMedium,
			Title:        fmt.Sprintf("Stopped instance still paying $%.2f/mo for its disks", ebsMonthly),
			Detail: fmt.Sprintf("The instance is stopped but its %d GB of EBS keeps billing. "+
				"Snapshots of the same data would cost about $%.2f/mo.", gb, snapshotAlt),
			Recommendation:    "If it's staying stopped, snapshot the volumes and terminate. If it runs on a schedule, automate stop/start.",
			MonthlySavingsUSD: saving,
		})
	}
	return out, nil
}

func nameTag(tags []ec2types.Tag) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == "Name" {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
