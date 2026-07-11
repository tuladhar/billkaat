//go:build pro

package pro

// This file is the worked example of a real Pro check. In your published
// repo, files like this one live only in the private overlay; the public
// repo ships catalog.go + locked_stub.go and nothing else from this package.

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

const natIdleThresholdBytes = 512 * 1024 * 1024 // < 512 MB out in 14 days = idle

type natIdle struct{}

func init() { checks.Register(natIdle{}) }

func (natIdle) Meta() checks.Meta { return metaFor("nat-idle") }

func (natIdle) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	var out []checks.Finding
	p := ec2.NewDescribeNatGatewaysPaginator(rc.AWS.EC2, &ec2.DescribeNatGatewaysInput{
		Filter: []ec2types.Filter{
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	end := time.Now()
	start := end.Add(-14 * 24 * time.Hour)

	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, gw := range page.NatGateways {
			id := aws.ToString(gw.NatGatewayId)
			stats, err := rc.AWS.CW.GetMetricStatistics(rc.Ctx, &cloudwatch.GetMetricStatisticsInput{
				Namespace:  aws.String("AWS/NATGateway"),
				MetricName: aws.String("BytesOutToDestination"),
				Dimensions: []cwtypes.Dimension{
					{Name: aws.String("NatGatewayId"), Value: aws.String(id)},
				},
				StartTime:  aws.Time(start),
				EndTime:    aws.Time(end),
				Period:     aws.Int32(86400),
				Statistics: []cwtypes.Statistic{cwtypes.StatisticSum},
			})
			if err != nil {
				return nil, err
			}
			var total float64
			for _, dp := range stats.Datapoints {
				total += aws.ToFloat64(dp.Sum)
			}
			if total >= natIdleThresholdBytes {
				continue
			}
			out = append(out, checks.Finding{
				ResourceID:   id,
				ResourceType: "NAT Gateway",
				Severity:     checks.SevHigh,
				Title:        fmt.Sprintf("NAT gateway pushed only %s in the last 14 days", humanBytes(total)),
				Detail: fmt.Sprintf("A NAT gateway bills ~$%.2f/mo plus $0.045/GB processed, "+
					"whether or not anything uses it.", checks.PriceNATMonthly),
				Recommendation: "If nothing in the private subnets needs internet egress, delete it. " +
					"If only AWS APIs are needed, VPC endpoints are usually cheaper.",
				MonthlySavingsUSD: checks.PriceNATMonthly,
			})
		}
	}
	return out, nil
}

func humanBytes(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", b/(1<<10))
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}
