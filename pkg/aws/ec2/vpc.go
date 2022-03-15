package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pkg/errors"
)

type EC2Client interface {
	DescribeVpcs(ctx context.Context, input *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error)
	CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
	ModifyLaunchTemplate(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error)
}

func NewEC2Client(cfg aws.Config) EC2Client {
	return ec2.NewFromConfig(cfg)
}

func GetVPCIdFromCIDR(ctx context.Context, ec2Client EC2Client, CIDR string) (*string, error) {

	filter := "cidr"
	result, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{
				Name: &filter,
				Values: []string{
					CIDR,
				},
			},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to describe VPCs")
	}

	if len(result.Vpcs) == 0 {
		return nil, errors.Wrap(errors.Errorf("VPC Not Found"), "failed to retrieve vpc with CIDR")
	}

	return result.Vpcs[0].VpcId, nil
}

func GetLastLaunchTemplateVersion(ctx context.Context, ec2Client EC2Client, launchTemplateID string) (*ec2types.LaunchTemplateVersion, error) {

	input := &ec2.DescribeLaunchTemplateVersionsInput{
		LaunchTemplateId: aws.String(launchTemplateID),
		Versions: []string{
			"$Latest",
		},
	}

	result, err := ec2Client.DescribeLaunchTemplateVersions(ctx, input)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to retrieve Launch Template %s", launchTemplateID))
	}

	if len(result.LaunchTemplateVersions) == 0 {
		return nil, errors.Wrap(errors.Errorf("Launch Template Not Found"), fmt.Sprintf("failed to retrieve LaunchTemplate %s", launchTemplateID))
	}

	return &result.LaunchTemplateVersions[0], nil
}

func AttachSecurityGroupToLaunchTemplate(ctx context.Context, ec2Client EC2Client, securityGroupId string, launchTemplateVersion *ec2types.LaunchTemplateVersion) (*ec2.CreateLaunchTemplateVersionOutput, error) {

	if len(launchTemplateVersion.LaunchTemplateData.SecurityGroupIds) == 0 {
		return nil, fmt.Errorf("failed to retrieve sgIds from LaunchTemplate %s", *launchTemplateVersion.LaunchTemplateId)
	}

	sgIds := append(launchTemplateVersion.LaunchTemplateData.SecurityGroupIds, securityGroupId)

	input := &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			SecurityGroupIds: sgIds,
		},
		LaunchTemplateId: aws.String(*launchTemplateVersion.LaunchTemplateId),
		SourceVersion:    aws.String("$Latest"),
	}

	output, err := ec2Client.CreateLaunchTemplateVersion(ctx, input)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to create version in Launch Template %s", *launchTemplateVersion.LaunchTemplateId))
	}

	return output, nil
}

func UpdateLaunchTemplateDefaultVersion(ctx context.Context, ec2Client EC2Client, launchTemplateID string) (*ec2types.LaunchTemplate, error) {
	input := &ec2.ModifyLaunchTemplateInput{
		DefaultVersion:   aws.String("$Latest"),
		LaunchTemplateId: aws.String(launchTemplateID),
	}

	lt, err := ec2Client.ModifyLaunchTemplate(ctx, input)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to modify LaunchTemplate %s", launchTemplateID))
	}
	return lt.LaunchTemplate, nil
}
