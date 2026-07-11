package free

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

// ---- gp2 → gp3 ----

type ebsGp2 struct{}

func init() { checks.Register(ebsGp2{}) }

func (ebsGp2) Meta() checks.Meta {
	return checks.Meta{
		ID:          "ebs-gp2-to-gp3",
		Name:        "gp2 volumes not migrated to gp3",
		Description: "gp3 is ~20% cheaper per GB than gp2 and migration is an online, no-downtime change.",
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (ebsGp2) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	var out []checks.Finding
	p := ec2.NewDescribeVolumesPaginator(rc.AWS.EC2, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("volume-type"), Values: []string{"gp2"}},
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Volumes {
			size := int64(aws.ToInt32(v.Size))
			saving := float64(size) * (checks.EBSPricePerGBMonth("gp2") - checks.EBSPricePerGBMonth("gp3"))
			detail := "gp3 costs ~$0.08/GB-month vs ~$0.10 for gp2 and includes a 3,000 IOPS / 125 MB/s baseline."
			if size > 1000 {
				detail += " This volume is over 1 TB, so match its gp2 baseline IOPS by provisioning extra IOPS on gp3 (this reduces, but rarely erases, the saving)."
			}
			out = append(out, checks.Finding{
				ResourceID:        aws.ToString(v.VolumeId),
				ResourceType:      "EBS Volume",
				Severity:          checks.SevLow,
				Title:             fmt.Sprintf("%d GB gp2 volume can migrate to gp3", size),
				Detail:            detail,
				Recommendation:    "Modify the volume type to gp3 (ModifyVolume) — it's an online operation with no downtime.",
				MonthlySavingsUSD: saving,
			})
		}
	}
	return out, nil
}

// ---- unassociated Elastic IPs ----

type eipUnassociated struct{}

func init() { checks.Register(eipUnassociated{}) }

func (eipUnassociated) Meta() checks.Meta {
	return checks.Meta{
		ID:          "eip-unassociated",
		Name:        "Idle Elastic IPs",
		Description: "Allocated public IPv4 addresses that are attached to nothing bill $0.005/hour each.",
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (eipUnassociated) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	out, err := rc.AWS.EC2.DescribeAddresses(rc.Ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		return nil, err
	}
	var fs []checks.Finding
	for _, a := range out.Addresses {
		if a.AssociationId != nil || a.InstanceId != nil || a.NetworkInterfaceId != nil {
			continue
		}
		id := aws.ToString(a.PublicIp)
		if alloc := aws.ToString(a.AllocationId); alloc != "" {
			id = fmt.Sprintf("%s (%s)", id, alloc)
		}
		fs = append(fs, checks.Finding{
			ResourceID:        id,
			ResourceType:      "Elastic IP",
			Severity:          checks.SevMedium,
			Title:             "Elastic IP allocated but not attached to anything",
			Detail:            "Idle public IPv4 addresses are billed at $0.005/hour whether or not you use them.",
			Recommendation:    "Release the address, or attach it to the resource that needs it.",
			MonthlySavingsUSD: checks.PriceEIPMonthly,
		})
	}
	return fs, nil
}
