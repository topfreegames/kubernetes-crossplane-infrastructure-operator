package dryrun

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"

	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
)

// EC2ClientFactory wraps f so that the returned clients read from AWS and only
// log writes.
func EC2ClientFactory(f func(cfg aws.Config) ec2.EC2Client) func(cfg aws.Config) ec2.EC2Client {
	return func(cfg aws.Config) ec2.EC2Client {
		return &EC2Client{EC2Client: f(cfg)}
	}
}

// AutoScalingClientFactory wraps f so that the returned clients read from AWS
// and only log writes.
func AutoScalingClientFactory(f func(cfg aws.Config) autoscaling.AutoScalingClient) func(cfg aws.Config) autoscaling.AutoScalingClient {
	return func(cfg aws.Config) autoscaling.AutoScalingClient {
		return &AutoScalingClient{AutoScalingClient: f(cfg)}
	}
}

// OceanClientFactory wraps f so that the returned clients read from Spot and
// only log writes.
func OceanClientFactory(f func() spot.OceanClient) func() spot.OceanClient {
	return func() spot.OceanClient {
		return &OceanClient{OceanClient: f()}
	}
}

type EC2Client struct {
	ec2.EC2Client
}

// CreateLaunchTemplateVersion returns the version that AWS would create: the
// latest version of the launch template plus one.
func (c *EC2Client) CreateLaunchTemplateVersion(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns ...func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
	logger(ctx).Info("would write", "verb", "CreateLaunchTemplateVersion",
		"launchTemplateId", aws.ToString(params.LaunchTemplateId),
		"launchTemplateName", aws.ToString(params.LaunchTemplateName),
		"sourceVersion", aws.ToString(params.SourceVersion),
		"input", params,
	)

	latest, err := c.EC2Client.DescribeLaunchTemplateVersions(ctx, &awsec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateId:   params.LaunchTemplateId,
		LaunchTemplateName: params.LaunchTemplateName,
		Versions:           []string{"$Latest"},
	}, optFns...)
	if err != nil {
		return nil, err
	}
	if len(latest.LaunchTemplateVersions) == 0 {
		return nil, fmt.Errorf("launch template %s%s has no versions", aws.ToString(params.LaunchTemplateId), aws.ToString(params.LaunchTemplateName))
	}
	current := latest.LaunchTemplateVersions[0]

	return &awsec2.CreateLaunchTemplateVersionOutput{
		LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
			LaunchTemplateId:   current.LaunchTemplateId,
			LaunchTemplateName: current.LaunchTemplateName,
			VersionNumber:      aws.Int64(aws.ToInt64(current.VersionNumber) + 1),
			DefaultVersion:     aws.Bool(false),
		},
	}, nil
}

func (c *EC2Client) ModifyInstanceAttribute(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, optFns ...func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
	logger(ctx).Info("would write", "verb", "ModifyInstanceAttribute",
		"instanceId", aws.ToString(params.InstanceId),
		"groups", params.Groups,
		"input", params,
	)
	return &awsec2.ModifyInstanceAttributeOutput{}, nil
}

type AutoScalingClient struct {
	autoscaling.AutoScalingClient
}

func (c *AutoScalingClient) UpdateAutoScalingGroup(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns ...func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
	kv := []interface{}{"verb", "UpdateAutoScalingGroup", "autoScalingGroupName", aws.ToString(params.AutoScalingGroupName)}
	if lt := params.LaunchTemplate; lt != nil {
		kv = append(kv, "launchTemplateId", aws.ToString(lt.LaunchTemplateId), "launchTemplateVersion", aws.ToString(lt.Version))
	}
	logger(ctx).Info("would write", append(kv, "input", params)...)
	return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
}

type OceanClient struct {
	spot.OceanClient
}

func (c *OceanClient) UpdateLaunchSpec(ctx context.Context, params *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
	kv := []interface{}{"verb", "UpdateLaunchSpec"}
	if ls := params.LaunchSpec; ls != nil {
		kv = append(kv, "launchSpecId", aws.ToString(ls.ID), "launchSpecName", aws.ToString(ls.Name), "securityGroupIds", ls.SecurityGroupIDs)
	}
	logger(ctx).Info("would write", kv...)
	return &oceanaws.UpdateLaunchSpecOutput{LaunchSpec: params.LaunchSpec}, nil
}
