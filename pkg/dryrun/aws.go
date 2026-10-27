package dryrun

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"

	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
)

// The clients below do not embed the real clients: a method added to an
// interface must be added here too, or the build fails.

// EC2ClientFactory wraps f so that the returned clients read from AWS and only
// log writes.
func EC2ClientFactory(f func(cfg aws.Config) ec2.EC2Client) func(cfg aws.Config) ec2.EC2Client {
	return func(cfg aws.Config) ec2.EC2Client {
		return &EC2Client{real: f(cfg)}
	}
}

// AutoScalingClientFactory wraps f so that the returned clients read from AWS
// and only log writes.
func AutoScalingClientFactory(f func(cfg aws.Config) autoscaling.AutoScalingClient) func(cfg aws.Config) autoscaling.AutoScalingClient {
	return func(cfg aws.Config) autoscaling.AutoScalingClient {
		return &AutoScalingClient{real: f(cfg)}
	}
}

// OceanClientFactory wraps f so that the returned clients read from Spot and
// only log writes.
func OceanClientFactory(f func() spot.OceanClient) func() spot.OceanClient {
	return func() spot.OceanClient {
		return &OceanClient{real: f()}
	}
}

type EC2Client struct {
	real ec2.EC2Client
}

func (c *EC2Client) DescribeVpcs(ctx context.Context, params *awsec2.DescribeVpcsInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
	return c.real.DescribeVpcs(ctx, params, optFns...)
}

func (c *EC2Client) DescribeInstances(ctx context.Context, params *awsec2.DescribeInstancesInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
	return c.real.DescribeInstances(ctx, params, optFns...)
}

func (c *EC2Client) DescribeLaunchTemplateVersions(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
	return c.real.DescribeLaunchTemplateVersions(ctx, params, optFns...)
}

func (c *EC2Client) DescribeSecurityGroups(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
	return c.real.DescribeSecurityGroups(ctx, params, optFns...)
}

func (c *EC2Client) DescribeRouteTables(ctx context.Context, params *awsec2.DescribeRouteTablesInput, optFns ...func(*awsec2.Options)) (*awsec2.DescribeRouteTablesOutput, error) {
	return c.real.DescribeRouteTables(ctx, params, optFns...)
}

// CreateLaunchTemplateVersion returns the version that AWS would create: the
// latest version of the launch template plus one.
func (c *EC2Client) CreateLaunchTemplateVersion(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns ...func(*awsec2.Options)) (_ *awsec2.CreateLaunchTemplateVersionOutput, err error) {
	defer func() { recordCloudWriteError(ctx, "CreateLaunchTemplateVersion", err) }()
	var securityGroups [][]string
	if params.LaunchTemplateData != nil {
		for _, ni := range params.LaunchTemplateData.NetworkInterfaces {
			securityGroups = append(securityGroups, ni.Groups)
		}
	}
	logger(ctx).Info("would write", "verb", "CreateLaunchTemplateVersion",
		"launchTemplateId", aws.ToString(params.LaunchTemplateId),
		"sourceVersion", aws.ToString(params.SourceVersion),
		"networkInterfaceSecurityGroups", securityGroups,
	)

	latest, err := c.real.DescribeLaunchTemplateVersions(ctx, &awsec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateId:   params.LaunchTemplateId,
		LaunchTemplateName: params.LaunchTemplateName,
		Versions:           []string{"$Latest"},
	}, optFns...)
	if err != nil {
		return nil, err
	}
	if len(latest.LaunchTemplateVersions) == 0 {
		return nil, fmt.Errorf("launch template %s has no versions", aws.ToString(params.LaunchTemplateId))
	}
	current := latest.LaunchTemplateVersions[0]

	if p, source, ok := planFrom(ctx); ok {
		var before, after []string
		if data := current.LaunchTemplateData; data != nil && len(data.NetworkInterfaces) > 0 {
			before = data.NetworkInterfaces[0].Groups
		}
		if len(securityGroups) > 0 {
			after = securityGroups[0]
		}
		version := aws.ToInt64(current.VersionNumber)
		p.RecordCloudChange(source,
			fmt.Sprintf("~ LaunchTemplate %s (CreateLaunchTemplateVersion)", aws.ToString(current.LaunchTemplateId)),
			fmt.Sprintf("-   version %d, security groups %s", version, groupList(before)),
			fmt.Sprintf("+   version %d, security groups %s", version+1, groupList(after)),
		)
	}

	return &awsec2.CreateLaunchTemplateVersionOutput{
		LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
			LaunchTemplateId:   current.LaunchTemplateId,
			LaunchTemplateName: current.LaunchTemplateName,
			VersionNumber:      aws.Int64(aws.ToInt64(current.VersionNumber) + 1),
		},
	}, nil
}

func (c *EC2Client) ModifyInstanceAttribute(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, optFns ...func(*awsec2.Options)) (_ *awsec2.ModifyInstanceAttributeOutput, err error) {
	defer func() { recordCloudWriteError(ctx, "ModifyInstanceAttribute", err) }()
	logger(ctx).Info("would write", "verb", "ModifyInstanceAttribute",
		"instanceId", aws.ToString(params.InstanceId),
		"groups", params.Groups,
	)
	if p, source, ok := planFrom(ctx); ok {
		output, err := c.real.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{InstanceIds: []string{aws.ToString(params.InstanceId)}}, optFns...)
		if err != nil {
			return nil, fmt.Errorf("dry-run plan: reading instance %s: %w", aws.ToString(params.InstanceId), err)
		}
		var before []string
		for _, r := range output.Reservations {
			for _, i := range r.Instances {
				for _, g := range i.SecurityGroups {
					before = append(before, aws.ToString(g.GroupId))
				}
			}
		}
		if groupList(before) != groupList(params.Groups) {
			p.RecordCloudChange(source,
				fmt.Sprintf("~ Instance %s (ModifyInstanceAttribute)", aws.ToString(params.InstanceId)),
				"-   security groups "+groupList(before),
				"+   security groups "+groupList(params.Groups),
			)
		}
	}
	return &awsec2.ModifyInstanceAttributeOutput{}, nil
}

type AutoScalingClient struct {
	real autoscaling.AutoScalingClient
}

func (c *AutoScalingClient) DescribeAutoScalingGroups(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
	return c.real.DescribeAutoScalingGroups(ctx, params, optFns...)
}

func (c *AutoScalingClient) UpdateAutoScalingGroup(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns ...func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
	kv := []interface{}{"verb", "UpdateAutoScalingGroup", "autoScalingGroupName", aws.ToString(params.AutoScalingGroupName)}
	if lt := params.LaunchTemplate; lt != nil {
		kv = append(kv, "launchTemplateId", aws.ToString(lt.LaunchTemplateId), "launchTemplateVersion", aws.ToString(lt.Version))
	}
	logger(ctx).Info("would write", kv...)
	if p, source, ok := planFrom(ctx); ok && params.LaunchTemplate != nil {
		p.RecordCloudChange(source,
			fmt.Sprintf("~ AutoScalingGroup %s (UpdateAutoScalingGroup)", aws.ToString(params.AutoScalingGroupName)),
			fmt.Sprintf("+   launch template %s version %s", aws.ToString(params.LaunchTemplate.LaunchTemplateId), aws.ToString(params.LaunchTemplate.Version)),
		)
	}
	return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
}

type OceanClient struct {
	real spot.OceanClient
}

func (c *OceanClient) ListClusters(ctx context.Context, params *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
	return c.real.ListClusters(ctx, params)
}

func (c *OceanClient) ListLaunchSpecs(ctx context.Context, params *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
	return c.real.ListLaunchSpecs(ctx, params)
}

func (c *OceanClient) UpdateLaunchSpec(ctx context.Context, params *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
	kv := []interface{}{"verb", "UpdateLaunchSpec"}
	if ls := params.LaunchSpec; ls != nil {
		kv = append(kv, "launchSpecId", aws.ToString(ls.ID), "launchSpecName", aws.ToString(ls.Name), "securityGroupIds", ls.SecurityGroupIDs)
	}
	logger(ctx).Info("would write", kv...)
	if p, source, ok := planFrom(ctx); ok && params.LaunchSpec != nil {
		// The reconciler clears the Ocean ID before the update, so the current
		// launch spec cannot be read here.
		p.RecordCloudChange(source,
			fmt.Sprintf("~ OceanLaunchSpec %s %s (UpdateLaunchSpec)", aws.ToString(params.LaunchSpec.ID), aws.ToString(params.LaunchSpec.Name)),
			"+   security groups "+groupList(params.LaunchSpec.SecurityGroupIDs),
		)
	}
	return &oceanaws.UpdateLaunchSpecOutput{}, nil
}

// recordCloudWriteError adds a failed AWS or Spot write to the plan of ctx.
// The reconcilers turn some of these errors into events only, so the plan
// must see them itself.
func recordCloudWriteError(ctx context.Context, call string, err error) {
	if p, source, ok := planFrom(ctx); ok && err != nil {
		p.recordWriteError(source, fmt.Errorf("%s: %w", call, err))
	}
}

// groupList formats security group IDs sorted, so that the plan does not
// depend on their order.
func groupList(ids []string) string {
	sorted := append([]string{}, ids...)
	sort.Strings(sorted)
	return "[" + strings.Join(sorted, ", ") + "]"
}
