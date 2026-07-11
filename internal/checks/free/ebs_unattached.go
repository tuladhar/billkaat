// Package free contains the open-source check set. Every file follows the
// same shape: a struct, Meta(), Run(), and a Register call in init().
package free

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

type ebsUnattached struct{}

func init() { checks.Register(ebsUnattached{}) }

func (ebsUnattached) Meta() checks.Meta {
	return checks.Meta{
		ID:          "ebs-unattached",
		Name:        "Unattached EBS volumes",
		Description: "Volumes in 'available' state are attached to nothing but billed every hour.",
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (ebsUnattached) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	var out []checks.Finding
	p := ec2.NewDescribeVolumesPaginator(rc.AWS.EC2, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("status"), Values: []string{"available"}},
		},
	})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Volumes {
			size := int64(aws.ToInt32(v.Size))
			vt := string(v.VolumeType)
			monthly := float64(size) * checks.EBSPricePerGBMonth(vt)
			created := aws.ToTime(v.CreateTime).Format("2006-01-02")
			out = append(out, checks.Finding{
				ResourceID:   aws.ToString(v.VolumeId),
				ResourceType: "EBS Volume",
				Severity:     checks.SevHigh,
				Title:        fmt.Sprintf("%d GB %s volume attached to nothing", size, vt),
				Detail: fmt.Sprintf("Volume has status 'available' (created %s). "+
					"You pay for it every hour even though no instance uses it.", created),
				Recommendation:    "Snapshot it if you might need the data, then delete the volume.",
				MonthlySavingsUSD: monthly,
			})
		}
	}
	return out, nil
}
