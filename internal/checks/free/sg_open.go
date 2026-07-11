package free

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/billkaat/billkaat/internal/checks"
)

var sensitivePorts = map[int32]string{
	22:    "SSH",
	3389:  "RDP",
	3306:  "MySQL",
	5432:  "PostgreSQL",
	6379:  "Redis",
	27017: "MongoDB",
	9200:  "Elasticsearch",
	1433:  "SQL Server",
	5601:  "Kibana",
	2375:  "Docker API",
}

// openPortHits returns the sensitive services covered by a from-to port
// range. allPorts covers everything (protocol -1 or a nil port range).
func openPortHits(from, to int32, allPorts bool) []string {
	var hits []string
	for port, name := range sensitivePorts {
		if allPorts || (from <= port && port <= to) {
			hits = append(hits, fmt.Sprintf("%s (%d)", name, port))
		}
	}
	sort.Strings(hits)
	return hits
}

type sgOpenToWorld struct{}

func init() { checks.Register(sgOpenToWorld{}) }

func (sgOpenToWorld) Meta() checks.Meta {
	return checks.Meta{
		ID:          "sg-open-to-world",
		Name:        "Security groups open to the internet",
		Description: "Inbound rules allowing 0.0.0.0/0 or ::/0 on SSH, RDP, databases, or all traffic.",
		Category:    checks.CatSecurity,
		Tier:        checks.TierFree,
	}
}

func (sgOpenToWorld) Run(rc *checks.RunContext) ([]checks.Finding, error) {
	var out []checks.Finding
	p := ec2.NewDescribeSecurityGroupsPaginator(rc.AWS.EC2, &ec2.DescribeSecurityGroupsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(rc.Ctx)
		if err != nil {
			return nil, err
		}
		for _, sg := range page.SecurityGroups {
			label := aws.ToString(sg.GroupId)
			if n := aws.ToString(sg.GroupName); n != "" {
				label = fmt.Sprintf("%s (%s)", label, n)
			}
			for _, perm := range sg.IpPermissions {
				if !worldOpen(perm) {
					continue
				}
				proto := aws.ToString(perm.IpProtocol)
				allPorts := proto == "-1" || perm.FromPort == nil
				if allPorts {
					out = append(out, checks.Finding{
						ResourceID:   label,
						ResourceType: "Security Group",
						Severity:     checks.SevCritical,
						Title:        "ALL traffic open to the entire internet",
						Detail:       "An inbound rule allows every port and protocol from 0.0.0.0/0.",
						Recommendation: "Remove the allow-all rule and open only the specific " +
							"ports each service needs, restricted to known CIDRs where possible.",
					})
					continue
				}
				hits := openPortHits(aws.ToInt32(perm.FromPort), aws.ToInt32(perm.ToPort), false)
				if len(hits) == 0 {
					continue // 80/443-style public rules are normal
				}
				out = append(out, checks.Finding{
					ResourceID:   label,
					ResourceType: "Security Group",
					Severity:     checks.SevHigh,
					Title:        strings.Join(hits, ", ") + " open to the entire internet",
					Detail:       "An inbound rule allows 0.0.0.0/0 (or ::/0) on a sensitive port. Bots scan for these constantly.",
					Recommendation: "Restrict the rule to your office/VPN CIDR, or drop it entirely " +
						"and use SSM Session Manager for shell access.",
				})
			}
		}
	}
	return out, nil
}

func worldOpen(perm ec2types.IpPermission) bool {
	for _, r := range perm.IpRanges {
		if aws.ToString(r.CidrIp) == "0.0.0.0/0" {
			return true
		}
	}
	for _, r := range perm.Ipv6Ranges {
		if aws.ToString(r.CidrIpv6) == "::/0" {
			return true
		}
	}
	return false
}
