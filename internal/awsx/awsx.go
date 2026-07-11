// Package awsx wraps AWS SDK client construction. Everything is read-only;
// the tool never calls a mutating API.
package awsx

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Credentials is the static access key pair for one saved AWS account. The
// tool no longer reads AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY from the
// environment — every scan is tied to an account the user added in the UI.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

// Clients bundles the service clients checks are allowed to use.
type Clients struct {
	Region string
	Cfg    aws.Config
	EC2    *ec2.Client
	ELBv2  *elbv2.Client
	CW     *cloudwatch.Client
	STS    *sts.Client
}

// New builds clients for the given region using the static credentials of a
// saved AWS account.
func New(ctx context.Context, region string, creds Credentials) (*Clients, error) {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, fmt.Errorf("no AWS account selected")
	}
	opts := []func(*config.LoadOptions) error{
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.SecretAccessKey, "")),
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("no AWS region configured: pick one in the UI or set AWS_REGION")
	}
	return &Clients{
		Region: cfg.Region,
		Cfg:    cfg,
		EC2:    ec2.NewFromConfig(cfg),
		ELBv2:  elbv2.NewFromConfig(cfg),
		CW:     cloudwatch.NewFromConfig(cfg),
		STS:    sts.NewFromConfig(cfg),
	}, nil
}

// Identity is the caller identity shown in the UI so people can confirm
// which account they are about to scan.
type Identity struct {
	Account string `json:"account"`
	Arn     string `json:"arn"`
}

// Identity resolves the current credentials via STS.
func (c *Clients) Identity(ctx context.Context) (*Identity, error) {
	out, err := c.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, err
	}
	return &Identity{Account: aws.ToString(out.Account), Arn: aws.ToString(out.Arn)}, nil
}

// DefaultRegion returns the region from the environment, falling back to
// ap-south-1 (Mumbai), the closest region for most Nepali workloads.
func DefaultRegion() string {
	for _, k := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "ap-south-1"
}

// Regions is the list offered in the UI dropdown. Any region string typed by
// the user is also accepted by the API.
var Regions = []string{
	"ap-south-1", "ap-southeast-1", "ap-southeast-2", "ap-northeast-1",
	"us-east-1", "us-east-2", "us-west-1", "us-west-2",
	"eu-west-1", "eu-west-2", "eu-central-1",
	"ca-central-1", "sa-east-1", "me-south-1",
}
