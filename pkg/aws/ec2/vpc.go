package ec2

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
)

type EC2Client interface {
	DescribeVpcs(ctx context.Context, input *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeLaunchTemplates(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error)
	CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
	ModifyInstanceAttribute(ctx context.Context, params *ec2.ModifyInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error)
	DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	DescribeRouteTables(ctx context.Context, input *ec2.DescribeRouteTablesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
}

func NewEC2Client(cfg aws.Config) EC2Client {
	return ec2.NewFromConfig(cfg)
}

func isSGAttached(sgs []ec2types.GroupIdentifier, sgID string) bool {
	for _, sg := range sgs {
		if *sg.GroupId == sgID {
			return true
		}
	}

	return false
}

func getInstances(ctx context.Context, instanceIDs []string, ec2Client EC2Client) ([]*ec2types.Instance, error) {
	var instances []*ec2types.Instance

	params := &ec2.DescribeInstancesInput{
		InstanceIds: instanceIDs,
	}

	paginator := ec2.NewDescribeInstancesPaginator(ec2Client, params)

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				instances = append(instances, &instance)
			}
		}
	}
	return instances, nil
}

func AttachSecurityGroupToInstances(ctx context.Context, ec2Client EC2Client, instanceIDs []string, securityGroupID string) error {

	if len(instanceIDs) == 0 {
		return nil
	}

	instances, err := getInstances(ctx, instanceIDs, ec2Client)
	if err != nil {
		return fmt.Errorf("error trying to retrieve instances: %v", err)
	}

	if len(instances) == 0 {
		return fmt.Errorf("failed to retrieve instances")
	}

	for _, instance := range instances {
		if instance.State.Name == ec2types.InstanceStateNameTerminated || isSGAttached(instance.SecurityGroups, securityGroupID) {
			continue
		}

		sgIDs := []string{}
		for _, sg := range instance.SecurityGroups {
			sgIDs = append(sgIDs, *sg.GroupId)
		}

		sgIDs = append(sgIDs, securityGroupID)

		_, err := ec2Client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
			InstanceId: instance.InstanceId,
			Groups:     sgIDs,
		})
		if err != nil {
			return fmt.Errorf("failed to add security group %s to instance %s: %v", securityGroupID, *instance.InstanceId, err)
		}
	}
	return nil
}

func DetachSecurityGroupFromInstances(ctx context.Context, ec2Client EC2Client, instanceIDs []string, securityGroupID string) error {

	if len(instanceIDs) == 0 {
		return nil
	}

	instances, err := getInstances(ctx, instanceIDs, ec2Client)
	if err != nil {
		return fmt.Errorf("error trying to retrieve instances: %v", err)
	}

	if len(instances) == 0 {
		return fmt.Errorf("failed to retrieve instances")
	}

	for _, instance := range instances {
		if instance.State.Name == ec2types.InstanceStateNameTerminated || !isSGAttached(instance.SecurityGroups, securityGroupID) {
			continue
		}

		sgIDs := []string{}
		for _, sg := range instance.SecurityGroups {
			if *sg.GroupId != securityGroupID {
				sgIDs = append(sgIDs, *sg.GroupId)
			}
		}

		_, err := ec2Client.ModifyInstanceAttribute(ctx, &ec2.ModifyInstanceAttributeInput{
			InstanceId: instance.InstanceId,
			Groups:     sgIDs,
		})
		if err != nil {
			return fmt.Errorf("failed to detach security group %s from instance %s: %v", securityGroupID, *instance.InstanceId, err)
		}
	}
	return nil
}

func GetVPCIdWithCIDRAndClusterName(ctx context.Context, ec2Client EC2Client, clusterName, CIDR string) (*string, error) {

	result, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []ec2types.Filter{
			{
				Name: aws.String("cidr"),
				Values: []string{
					CIDR,
				},
			},
			{
				Name: aws.String("tag:KubernetesCluster"),
				Values: []string{
					clusterName,
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

func CheckSecurityGroupExists(ctx context.Context, ec2Client EC2Client, sgId string) (bool, error) {
	input := &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{
			sgId,
		},
	}

	_, err := ec2Client.DescribeSecurityGroups(ctx, input)
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && apiError.ErrorCode() == "InvalidGroup.NotFound" {
			return false, nil
		} else {
			return false, err
		}
	}
	return true, nil
}

func AttachSecurityGroupToLaunchTemplate(ctx context.Context, ec2Client EC2Client, securityGroupId string, launchTemplateVersion *ec2types.LaunchTemplateVersion) (*ec2.CreateLaunchTemplateVersionOutput, error) {

	if len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces) == 0 || len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0].Groups) == 0 {
		return nil, fmt.Errorf("failed to retrieve SGs from LaunchTemplate %s", *launchTemplateVersion.LaunchTemplateId)
	}

	networkInterface := launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0]

	currentSecurityGroups := networkInterface.Groups
	sort.Strings(currentSecurityGroups)

	sgIds := []string{}
	for _, sgId := range currentSecurityGroups {
		ok, err := CheckSecurityGroupExists(ctx, ec2Client, sgId)
		if err != nil {
			return nil, err
		}
		if ok && sgId != securityGroupId {
			sgIds = append(sgIds, sgId)
		}
	}

	sgIds = append(sgIds, securityGroupId)
	sort.Strings(sgIds)

	if cmp.Equal(sgIds, currentSecurityGroups) {
		return &ec2.CreateLaunchTemplateVersionOutput{
			LaunchTemplateVersion: launchTemplateVersion,
		}, nil
	}

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

func DetachSecurityGroupFromLaunchTemplate(ctx context.Context, ec2Client EC2Client, securityGroupId string, launchTemplateVersion *ec2types.LaunchTemplateVersion) (*ec2.CreateLaunchTemplateVersionOutput, error) {

	if len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces) == 0 || len(launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0].Groups) == 0 {
		return nil, fmt.Errorf("failed to retrieve SGs from LaunchTemplate %s", *launchTemplateVersion.LaunchTemplateId)
	}

	networkInterface := launchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0]
	remainingSecurityGroups := []string{}
	for _, group := range networkInterface.Groups {
		if group != securityGroupId {
			remainingSecurityGroups = append(remainingSecurityGroups, group)
		}
	}

	networkInterface.Groups = remainingSecurityGroups

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

func GetLaunchTemplateFromInstanceGroup(ctx context.Context, ec2Client EC2Client, kubernetesClusterName, launchTemplateName string) (*ec2types.LaunchTemplate, error) {
	paginator := ec2.NewDescribeLaunchTemplatesPaginator(ec2Client, &ec2.DescribeLaunchTemplatesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:KubernetesCluster"),
				Values: []string{kubernetesClusterName},
			},
			{
				Name:   aws.String("tag:Name"),
				Values: []string{launchTemplateName},
			},
		},
	})

	var launchTemplates []ec2types.LaunchTemplate
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		launchTemplates = append(launchTemplates, output.LaunchTemplates...)
	}

	if len(launchTemplates) == 0 {
		return nil, fmt.Errorf("failed to get launch template")
	}

	return &launchTemplates[0], nil
}
