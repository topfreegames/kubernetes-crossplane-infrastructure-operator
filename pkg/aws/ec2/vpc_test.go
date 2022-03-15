package ec2

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2/fake"
)

func TestGetVPCIdFromCIDR(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return a VPC ID",
			"mockDescribeVpcs": func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("vpc-xxxxx"),
						},
					},
				}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should fail to describe VPCs",
			"mockDescribeVpcs": func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return nil, errors.New("some error")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to describe VPCs",
		},
		{
			"description": "should fail to with empty result",
			"mockDescribeVpcs": func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{},
				}, nil
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve vpc with CIDR",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = tc["mockDescribeVpcs"].(func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error))
			vpcId, err := GetVPCIdFromCIDR(ctx, fakeEC2Client, "x.x.x.x/20")

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(vpcId).ToNot(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}

func TestGetLastLaunchTemplateVersion(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return the latest template version",
			"mockDescribeLaunchTemplateVersions": func(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &ec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: aws.String("lt-xxxxx"),
						},
					},
				}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should fail to describe LTs",
			"mockDescribeLaunchTemplateVersions": func(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
				return nil, errors.New("some error")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve Launch Template",
		},
		{
			"description": "should fail with empty result",
			"mockDescribeLaunchTemplateVersions": func(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &ec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{},
				}, nil

			},
			"expectedError":        true,
			"expectedErrorMessage": "Launch Template Not Found",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = tc["mockDescribeLaunchTemplateVersions"].(func(ctx context.Context, params *ec2.DescribeLaunchTemplateVersionsInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error))
			ltVersion, err := GetLastLaunchTemplateVersion(ctx, fakeEC2Client, "lt-xxxxx")

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(ltVersion).ToNot(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}

func TestAttachSecurityGroupToLaunchTemplate(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should attach the securityGroup to LT",
			"launchTemplateVersion": &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-old",
							},
						},
					},
				},
			},
			"mockCreateLaunchTemplateVersion": func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
				return &ec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
							NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
								{
									Groups:           params.LaunchTemplateData.NetworkInterfaces[0].Groups,
									NetworkCardIndex: params.LaunchTemplateData.NetworkInterfaces[0].NetworkCardIndex,
								},
							},
						},
					},
				}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should fail without NetworkInterfaces",
			"launchTemplateVersion": &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{},
				},
			},
			"mockCreateLaunchTemplateVersion": func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
				return nil, nil
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve SGs from LaunchTemplate",
		},
		{
			"description": "should fail with empty groups in NetworkInterface",
			"launchTemplateVersion": &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{},
						},
					},
				},
			},
			"mockCreateLaunchTemplateVersion": func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
				return nil, nil
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve SGs from LaunchTemplate",
		},
		{
			"description": "should return error when failing to create LT version",
			"launchTemplateVersion": &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-old",
							},
						},
					},
				},
			},
			"mockCreateLaunchTemplateVersion": func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
				return nil, errors.New("some error")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to create version in Launch Template",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockCreateLaunchTemplateVersion = tc["mockCreateLaunchTemplateVersion"].(func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error))
			output, err := AttachSecurityGroupToLaunchTemplate(ctx, fakeEC2Client, "sg-new", tc["launchTemplateVersion"].(*ec2types.LaunchTemplateVersion))

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(output).ToNot(BeNil())
				g.Expect(output.LaunchTemplateVersion.LaunchTemplateData.NetworkInterfaces[0].Groups).To(Equal([]string{"sg-old", "sg-new"}))
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}

// func TestUpdateLaunchTemplateDefaultVersion(t *testing.T) {
// 	testCases := []map[string]interface{}{
// 		{
// 			"description": "should return the latest template version",
// 			"mockModifyLaunchTemplate": func(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns []func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
// 				return &ec2.ModifyLaunchTemplateOutput{
// 					LaunchTemplate: &ec2types.LaunchTemplate{
// 						LaunchTemplateId: aws.String("lt-xxxx"),
// 					},
// 				}, nil
// 			},
// 			"expectedError": false,
// 		},
// 		{
// 			"description": "should return the latest template version",
// 			"mockModifyLaunchTemplate": func(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns []func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error) {
// 				return nil, errors.New("some error")
// 			},
// 			"expectedError":        true,
// 			"expectedErrorMessage": "failed to modify LaunchTemplate",
// 		},
// 	}
// 	RegisterFailHandler(Fail)
// 	g := NewWithT(t)

// 	for _, tc := range testCases {
// 		t.Run(tc["description"].(string), func(t *testing.T) {
// 			ctx := context.TODO()
// 			fakeEC2Client := &fake.MockEC2Client{}
// 			fakeEC2Client.MockModifyLaunchTemplate = tc["mockModifyLaunchTemplate"].(func(ctx context.Context, params *ec2.ModifyLaunchTemplateInput, optFns []func(*ec2.Options)) (*ec2.ModifyLaunchTemplateOutput, error))
// 			lt, err := UpdateLaunchTemplateDefaultVersion(ctx, fakeEC2Client, "lt-xxxxx")

// 			if !tc["expectedError"].(bool) {
// 				g.Expect(err).To(BeNil())
// 				g.Expect(lt).ToNot(BeNil())
// 			} else {
// 				g.Expect(err).ToNot(BeNil())
// 				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
// 			}
// 		})
// 	}
// }
