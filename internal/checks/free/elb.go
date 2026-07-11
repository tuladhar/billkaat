package free

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

type lbSummary struct {
	name       string
	kind       string // "Application" | "Network"
	registered int
	healthy    int
	monthly    float64
}

// lbSummaries walks every ALB/NLB and counts registered and healthy targets.
func lbSummaries(rc *checks.RunContext) ([]lbSummary, error) {
	var out []lbSummary
	p := elbv2.NewDescribeLoadBalancersPaginator(rc.AWS.ELBv2, &elbv2.DescribeLoadBalancersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, lb := range page.LoadBalancers {
			s := lbSummary{name: aws.ToString(lb.LoadBalancerName)}
			switch lb.Type {
			case elbv2types.LoadBalancerTypeEnumApplication:
				s.kind, s.monthly = "Application", checks.PriceALBMonthly
			case elbv2types.LoadBalancerTypeEnumNetwork:
				s.kind, s.monthly = "Network", checks.PriceNLBMonthly
			default:
				continue // gateway LBs are out of scope
			}
			tgs, err := rc.AWS.ELBv2.DescribeTargetGroups(rc.Ctx, &elbv2.DescribeTargetGroupsInput{
				LoadBalancerArn: lb.LoadBalancerArn,
			})
			if err != nil {
				return nil, err
			}
			for _, tg := range tgs.TargetGroups {
				th, err := rc.AWS.ELBv2.DescribeTargetHealth(rc.Ctx, &elbv2.DescribeTargetHealthInput{
					TargetGroupArn: tg.TargetGroupArn,
				})
				if err != nil {
					return nil, err
				}
				s.registered += len(th.TargetHealthDescriptions)
				for _, d := range th.TargetHealthDescriptions {
					if d.TargetHealth != nil && d.TargetHealth.State == elbv2types.TargetHealthStateEnumHealthy {
						s.healthy++
					}
				}
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// ---- no registered targets: pure waste ----

type elbNoTargets struct{}

func init() { checks.Register(elbNoTargets{}) }

func (elbNoTargets) Meta() checks.Meta {
	return checks.Meta{
		ID:          "elb-no-targets",
		Name:        "Load balancers with zero targets",
		Description: "ALBs and NLBs bill ~$16/mo each just for existing. These forward traffic to nothing.",
		Category:    checks.CatCost,
		Tier:        checks.TierFree,
	}
}

func (elbNoTargets) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	sums, err := lbSummaries(rc)
	if err != nil {
		return nil, err
	}
	var out []checks.Finding
	for _, s := range sums {
		if s.registered != 0 {
			continue
		}
		out = append(out, checks.Finding{
			ResourceID:   s.name,
			ResourceType: s.kind + " Load Balancer",
			Severity:     checks.SevHigh,
			Title:        "Load balancer has zero registered targets",
			Detail: fmt.Sprintf("A %s load balancer bills ~$%.2f/mo (plus usage) just for existing. "+
				"This one has no targets registered in any target group.", s.kind, s.monthly),
			Recommendation:    "Delete the load balancer, or register the targets it was meant to serve.",
			MonthlySavingsUSD: s.monthly,
		})
	}
	return out, nil
}

// ---- registered but all unhealthy: an outage waiting to be noticed ----

type elbAllUnhealthy struct{}

func init() { checks.Register(elbAllUnhealthy{}) }

func (elbAllUnhealthy) Meta() checks.Meta {
	return checks.Meta{
		ID:          "elb-all-unhealthy",
		Name:        "Load balancers with no healthy targets",
		Description: "Targets are registered but every one of them is failing health checks — traffic has nowhere to go.",
		Category:    checks.CatPerformance,
		Tier:        checks.TierFree,
	}
}

func (elbAllUnhealthy) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	sums, err := lbSummaries(rc)
	if err != nil {
		return nil, err
	}
	var out []checks.Finding
	for _, s := range sums {
		if s.registered == 0 || s.healthy > 0 {
			continue
		}
		out = append(out, checks.Finding{
			ResourceID:   s.name,
			ResourceType: s.kind + " Load Balancer",
			Severity:     checks.SevCritical,
			Title:        fmt.Sprintf("All %d registered targets are unhealthy", s.registered),
			Detail:       "Every target behind this load balancer is failing its health check, so requests are being dropped or erroring.",
			Recommendation:    "Check the target application, its security groups, and the health-check path/port configured on the target group.",
			MonthlySavingsUSD: 0,
		})
	}
	return out, nil
}
