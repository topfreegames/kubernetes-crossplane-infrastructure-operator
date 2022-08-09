package fake

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

type MockEC2Client struct {
	MockDescribeVpcs                   func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	MockDescribeLaunchTemplateVersions func(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error)
	MockCreateLaunchTemplateVersion    func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
	MockModifyLaunchTemplate           func(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns []func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error)
	MockDescribeSecurityGroups         func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	MockDescribeRouteTables            func(ctx context.Context, params *ec2.DescribeRouteTablesInput, optFns []func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
}

func (m *MockEC2Client) DescribeVpcs(ctx context.Context, input *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return m.MockDescribeVpcs(ctx, input, opts)
}

func (m *MockEC2Client) DescribeLaunchTemplateVersions(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, opts ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	return m.MockDescribeLaunchTemplateVersions(ctx, params, opts)
}

func (m *MockEC2Client) CreateLaunchTemplateVersion(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, opts ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	return m.MockCreateLaunchTemplateVersion(ctx, params, opts)
}

func (m *MockEC2Client) ModifyLaunchTemplate(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, opts ...func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
	return m.MockModifyLaunchTemplate(ctx, params, opts)
}

func (m *MockEC2Client) DescribeSecurityGroups(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return m.MockDescribeSecurityGroups(ctx, params, opts)
}

func (m *MockEC2Client) DescribeRouteTables(ctx context.Context, params *ec2.DescribeRouteTablesInput, opts ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return m.MockDescribeRouteTables(ctx, params, opts)
}
