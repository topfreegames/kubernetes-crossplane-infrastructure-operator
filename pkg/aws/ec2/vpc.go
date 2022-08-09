package ec2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/pkg/errors"
)

type EC2Client interface {
	DescribeVpcs(ctx context.Context, input *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error)
	CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
	ModifyLaunchTemplate(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeRouteTables(ctx context.Context, input *ec2.DescribeRouteTablesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
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

func GetRouteTableIDsFromVPCId(ctx context.Context, ec2Client EC2Client, VPCId string) ([]string, error) {

	filter := "vpc-id"
	result, err := ec2Client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{
				Name: &filter,
				Values: []string{
					VPCId,
				},
			},
		},
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to describe route tables")
	}

	var routeTablesIDs []string

	for _, routeTable := range result.RouteTables {
		routeTablesIDs = append(routeTablesIDs, *routeTable.RouteTableId)
	}

	return routeTablesIDs, nil
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

func checkSecurityGroupExists(ctx context.Context, ec2Client EC2Client, sgId string) (bool, error) {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{
			sgId,
		},
	}

	_, err := ec2Client.DescribeSecurityGroups(ctx, input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case "InvalidGroup.NotFound":
				return false, nil
			default:
				return false, err
			}
		}
	}
	return true, nil
}

func AttachSecurityGroupToLaunchTemplate(ctx context.Context, ec2Client EC2Client, securityGroupId string, launchTemplateVersion *ec2types.LaunchTemplateVersion) (*ec2.CreateLaunchTemplateVersionOutput, error) {

	if len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces) == 0 || len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0].Groups) == 0 {
		return nil, fmt.Errorf("failed to retrieve SGs from LaunchTemplate %s", *launchTemplateVersion.LaunchTemplateId)
	}

	networkInterface := launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0]
	for _, group := range networkInterface.Groups {
		if group == securityGroupId {
			return &ec2.CreateLaunchTemplateVersionOutput{
				LaunchTemplateVersion: launchTemplateVersion,
			}, nil
		}
	}

	sgIds := []string{}
	for _, sgId := range networkInterface.Groups {
		ok, err := checkSecurityGroupExists(ctx, ec2Client, sgId)
		if err != nil {
			return nil, err
		}
		if ok {
			sgIds = append(sgIds, sgId)
		}
	}

	sgIds = append(sgIds, securityGroupId)

	networkInterface.Groups = sgIds

	b, err := json.Marshal(networkInterface)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal networkInterface")
	}

	networkInterfaceRequest := ec2types.LaunchTemplateInstanceNetworkInterfaceSpecificationRequest{}
	err = json.Unmarshal(b, &networkInterfaceRequest)
	if err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal networkInterfaceRequest")
	}

	input := &ec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecificationRequest{
				networkInterfaceRequest,
			},
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
