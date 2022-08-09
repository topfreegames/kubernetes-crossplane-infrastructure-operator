package ec2

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go/aws/awserr"
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

func TestCheckSecurityGroupExists(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return true with SG existing",
			"mockDescribeSecurityGroups": func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
				}, nil
			},
			"expectedError":  false,
			"expectedResult": true,
		},
		{
			"description": "should return false when not finding SG",
			"mockDescribeSecurityGroups": func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return nil, awserr.New("InvalidGroup.NotFound", "", errors.New("some error"))
			},
			"expectedError":  false,
			"expectedResult": false,
		},
		{
			"description": "should return false when not finding SG",
			"mockDescribeSecurityGroups": func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return nil, awserr.New("InvalidGroup.BadRequest", "", errors.New("some other error"))
			},
			"expectedError":  true,
			"expectedResult": false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeSecurityGroups = tc["mockDescribeSecurityGroups"].(func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error))
			result, err := checkSecurityGroupExists(ctx, fakeEC2Client, "sg-xxxxx")

			if tc["expectedError"].(bool) {
				g.Expect(err).ToNot(BeNil())
			}
			if tc["expectedResult"].(bool) {
				g.Expect(result).To(Equal(tc["expectedResult"].(bool)))
			}
		})
	}
}

func TestAttachSecurityGroupToLaunchTemplate(t *testing.T) {

	testCases := []struct {
		description                     string
		launchTemplateVersion           *ec2types.LaunchTemplateVersion
		mockCreateLaunchTemplateVersion func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error)
		mockDescribeSecurityGroups      func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
		expectedError                   bool
		expectedResult                  *ec2.CreateLaunchTemplateVersionOutput
		expectedErrorMessage            string
	}{
		{
			description: "should attach the securityGroup to LT",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				VersionNumber:    aws.Int64(1),
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
			expectedError: false,
			expectedResult: &ec2.CreateLaunchTemplateVersionOutput{
				LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
					LaunchTemplateId: aws.String("lt-xxxx"),
					VersionNumber:    aws.Int64(2),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-old",
									"sg-new",
								},
							},
						},
					},
				},
			},
		},
		{
			description: "should return the current LTVersion if SG already attached",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				VersionNumber:    aws.Int64(1),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-old",
								"sg-new",
							},
						},
					},
				},
			},
			expectedError: false,
			expectedResult: &ec2.CreateLaunchTemplateVersionOutput{
				LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
					LaunchTemplateId: aws.String("lt-xxxx"),
					VersionNumber:    aws.Int64(1),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-old",
									"sg-new",
								},
							},
						},
					},
				},
			},
		},
		{
			description: "should remove SGs that don't exist anymore",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				VersionNumber:    aws.Int64(1),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-old",
								"sg-removed",
							},
						},
					},
				},
			},
			mockDescribeSecurityGroups: func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				if params.GroupIds[0] == "sg-removed" {
					return nil, awserr.New("InvalidGroup.NotFound", "", errors.New("some error"))
				}
				return &ec2.DescribeSecurityGroupsOutput{}, nil
			},
			expectedError: false,
			expectedResult: &ec2.CreateLaunchTemplateVersionOutput{
				LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
					LaunchTemplateId: aws.String("lt-xxxx"),
					VersionNumber:    aws.Int64(2),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-old",
									"sg-new",
								},
							},
						},
					},
				},
			},
		},
		{
			description: "should fail without NetworkInterfaces",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{},
				},
			},
			expectedResult:       nil,
			expectedError:        true,
			expectedErrorMessage: "failed to retrieve SGs from LaunchTemplate",
		},
		{
			description: "should fail with empty groups in NetworkInterface",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{},
						},
					},
				},
			},
			expectedResult:       nil,
			expectedError:        true,
			expectedErrorMessage: "failed to retrieve SGs from LaunchTemplate",
		},
		{
			description: "should return error when failing to create LT version",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
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
			mockCreateLaunchTemplateVersion: func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
				return nil, errors.New("some error")
			},
			expectedResult:       nil,
			expectedError:        true,
			expectedErrorMessage: "failed to create version in Launch Template",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			if tc.mockCreateLaunchTemplateVersion == nil {
				fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *ec2.CreateLaunchTemplateVersionInput, optFns []func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
					var version int64
					if *params.SourceVersion == "$Latest" {
						version = 1
					} else {
						version, _ = strconv.ParseInt(*params.SourceVersion, 10, 64)
					}

					return &ec2.CreateLaunchTemplateVersionOutput{
						LaunchTemplateVersion: &ec2types.
							LaunchTemplateVersion{
							LaunchTemplateId: params.LaunchTemplateId,
							VersionNumber:    aws.Int64(version + 1),
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
				}
			} else {
				fakeEC2Client.MockCreateLaunchTemplateVersion = tc.mockCreateLaunchTemplateVersion

			}

			if tc.mockDescribeSecurityGroups == nil {
				fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
					return &ec2.DescribeSecurityGroupsOutput{}, nil
				}
			} else {
				fakeEC2Client.MockDescribeSecurityGroups = tc.mockDescribeSecurityGroups
			}

			output, err := AttachSecurityGroupToLaunchTemplate(ctx, fakeEC2Client, "sg-new", tc.launchTemplateVersion)
			if !tc.expectedError {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMessage))
			}
			g.Expect(output).To(BeEquivalentTo(tc.expectedResult))
		})
	}
}

func TestRouteTableIDsFromVPCId(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return a list of route table ids",
			"mockDescribeRouteTables": func(ctx context.Context, input *ec2.DescribeRouteTablesInput, opts []func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
				return &ec2.DescribeRouteTablesOutput{
					RouteTables: []ec2types.RouteTable{
						{
							RouteTableId: aws.String("rt-xxxxx"),
						},
						{
							RouteTableId: aws.String("rt-zzzzz"),
						},
					},
				}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should fail to describe route tables",
			"mockDescribeRouteTables": func(ctx context.Context, input *ec2.DescribeRouteTablesInput, opts []func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
				return nil, errors.New("some error")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to describe route tables",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeRouteTables = tc["mockDescribeRouteTables"].(func(ctx context.Context, input *ec2.DescribeRouteTablesInput, opts []func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error))
			routeTablesIDs, err := GetRouteTableIDsFromVPCId(ctx, fakeEC2Client, "vpc-aaaaa")

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				for _, routeTableID := range routeTablesIDs {
					g.Expect(routeTableID).ToNot(BeNil())
				}
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}
