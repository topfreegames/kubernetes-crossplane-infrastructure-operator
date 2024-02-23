package ec2

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
)

func TestGetVPCIdWithCIDRAndClusterName(t *testing.T) {
	testCases := []struct {
		description      string
		mockDescribeVpcs func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
		expectedError    error
		expectedCIDR     string
	}{
		{
			description: "should return vpc-xxxxx",
			mockDescribeVpcs: func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("vpc-xxxxx"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("KubernetesCluster"),
									Value: aws.String("test-cluster"),
								},
							},
						},
						{
							VpcId: aws.String("vpc-yyyyy"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("KubernetesCluster"),
									Value: aws.String("test-cluster-2"),
								},
							},
						},
					},
				}, nil
			},
			expectedCIDR: "vpc-xxxxx",
		},
		{
			description: "should fail to describe VPCs",
			mockDescribeVpcs: func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return nil, errors.New("some error")
			},
			expectedError: fmt.Errorf("failed to describe VPCs"),
		},
		{
			description: "should fail with empty result",
			mockDescribeVpcs: func(ctx context.Context, input *ec2.DescribeVpcsInput, opts []func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
				return &ec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{},
				}, nil
			},
			expectedError: fmt.Errorf("failed to retrieve vpc with CIDR"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = tc.mockDescribeVpcs
			vpcId, err := GetVPCIdWithCIDRAndClusterName(ctx, fakeEC2Client, "test-cluster", "x.x.x.x/20")

			if tc.expectedError != nil {
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError.Error()))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(*vpcId).To(BeEquivalentTo(tc.expectedCIDR))
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
	testCases := []struct {
		description                string
		mockDescribeSecurityGroups func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
		expectedError              bool
		expectedResult             bool
	}{
		{
			description: "should return true with SG existing",
			mockDescribeSecurityGroups: func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return &ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
				}, nil
			},
			expectedError:  false,
			expectedResult: true,
		},
		{
			description: "should return false when not finding SG",
			mockDescribeSecurityGroups: func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return nil, &smithy.GenericAPIError{
					Code:    "InvalidGroup.NotFound",
					Message: "some error",
					Fault:   smithy.FaultUnknown,
				}
			},
			expectedError:  false,
			expectedResult: false,
		},
		{
			description: "should return false when not finding SG",
			mockDescribeSecurityGroups: func(ctx context.Context, params *ec2.DescribeSecurityGroupsInput, optFns []func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
				return nil, &smithy.GenericAPIError{
					Code:    "InvalidGroup.BadRequest",
					Message: "some error",
					Fault:   smithy.FaultUnknown,
				}
			},
			expectedError:  true,
			expectedResult: false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeSecurityGroups = tc.mockDescribeSecurityGroups
			result, err := CheckSecurityGroupExists(ctx, fakeEC2Client, "sg-xxxxx")

			if tc.expectedError {
				g.Expect(err).ToNot(BeNil())
			} else {
				g.Expect(err).To(BeNil())
			}
			if tc.expectedResult {
				g.Expect(result).To(Equal(tc.expectedResult))
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
									"sg-new",
									"sg-old",
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
								"sg-new",
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
					VersionNumber:    aws.Int64(1),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-new",
									"sg-old",
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
					return nil, &smithy.GenericAPIError{
						Code:    "InvalidGroup.NotFound",
						Message: "some error",
						Fault:   smithy.FaultUnknown,
					}
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
									"sg-new",
									"sg-old",
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

func TestDetachSecurityGroupToLaunchTemplate(t *testing.T) {

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
			description: "should detach the securityGroup to LT",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				VersionNumber:    aws.Int64(1),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-1",
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
								Groups: []string{},
							},
						},
					},
				},
			},
		},
		{
			description: "should not return error if SG already detached from launch template",
			launchTemplateVersion: &ec2types.LaunchTemplateVersion{
				LaunchTemplateId: aws.String("lt-xxxx"),
				VersionNumber:    aws.Int64(1),
				LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
					NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
						{
							Groups: []string{
								"sg-2",
								"sg-3",
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
									"sg-2",
									"sg-3",
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

			output, err := DetachSecurityGroupFromLaunchTemplate(ctx, fakeEC2Client, "sg-1", tc.launchTemplateVersion)
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

func TestIsSGAttached(t *testing.T) {
	testCases := []struct {
		description string
		input       []ec2types.GroupIdentifier
		expected    bool
	}{
		{
			description: "should return true when the sg is already attached",
			input: []ec2types.GroupIdentifier{
				{
					GroupId: aws.String("sg-xxx"),
				},
			},
			expected: true,
		},
		{
			description: "should return true when the sg is already attached alongside with other sgs",
			input: []ec2types.GroupIdentifier{
				{
					GroupId: aws.String("sg-yyy"),
				},
				{
					GroupId: aws.String("sg-xxx"),
				},
			},
			expected: true,
		},
		{
			description: "should return false when the sg isn't attached",
			input: []ec2types.GroupIdentifier{
				{
					GroupId: aws.String("sg-yyy"),
				},
				{
					GroupId: aws.String("sg-zzz"),
				},
			},
			expected: false,
		},
		{
			description: "should return false when the sg list is empty",
			input:       []ec2types.GroupIdentifier{},
			expected:    false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			output := isSGAttached(tc.input, "sg-xxx")
			g.Expect(output).To(BeEquivalentTo(tc.expected))
		})
	}
}

func TestAttachSecurityGroupToInstances(t *testing.T) {
	testCases := []struct {
		description   string
		instances     []ec2types.Instance
		input         []string
		expected      map[string][]string
		expectedError error
	}{
		{
			description: "should attach sg-xxx to instance i-xxx",
			input:       []string{"i-xxx"},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-xxx", "sg-yyy"},
			},
		},
		{
			description: "should do nothing when sg-xxx is already attached in instance i-xxx",
			input:       []string{"i-xxx"},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxx"),
						},
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-xxx", "sg-yyy"},
			},
		},
		{
			description: "should do nothing if list of instance ids is empty",
			input:       []string{},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy"},
			},
		},
		{
			description: "should return error when can't find any instances",
			input:       []string{"i-zzz"},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedError: fmt.Errorf("failed to retrieve instances"),
		},
		{
			description: "should attach sg-xxx to multiple instances",
			input:       []string{"i-xxx", "i-yyy"},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
				{
					InstanceId: aws.String("i-yyy"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-xxx", "sg-yyy"},
				"i-yyy": {"sg-xxx", "sg-yyy"},
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *ec2.DescribeInstancesInput, opts []func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
				var instances []ec2types.Instance
				for _, instanceID := range tc.input {
					for _, instance := range tc.instances {
						if *instance.InstanceId == instanceID {
							instances = append(instances, instance)
						}
					}
				}
				return &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: instances,
						},
					},
				}, nil
			}

			sgsAttached := map[string][]string{}
			for _, instance := range tc.instances {
				for _, sg := range instance.SecurityGroups {
					sgsAttached[*instance.InstanceId] = append(sgsAttached[*instance.InstanceId], *sg.GroupId)
				}
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *ec2.ModifyInstanceAttributeInput, opts []func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error) {
				sgsAttached[*params.InstanceId] = params.Groups
				return &ec2.ModifyInstanceAttributeOutput{}, nil
			}

			err := AttachSecurityGroupToInstances(ctx, fakeEC2Client, tc.input, "sg-xxx")
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				for instanceId, sgs := range tc.expected {
					sort.Strings(sgs)
					sort.Strings(sgsAttached[instanceId])
					g.Expect(sgsAttached[instanceId]).To(BeEquivalentTo(sgs))
				}
			}
		})
	}
}

func TestDetachSecurityGroupFromInstances(t *testing.T) {
	testCases := []struct {
		description   string
		instances     []ec2types.Instance
		input         []string
		sg            string
		expected      map[string][]string
		expectedError error
	}{
		{
			description: "should detach sg-xxx from instance i-xxx",
			input:       []string{"i-xxx"},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
						{
							GroupId: aws.String("sg-xxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy"},
			},
		},
		{
			description: "should do nothing when sg-xxx is not attached in instance i-xxx",
			input:       []string{"i-xxx"},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy"},
			},
		},
		{
			description: "should do nothing if list of instance ids is empty",
			input:       []string{},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy"},
			},
		},
		{
			description: "should do nothing if the instance has no sgs",
			input:       []string{"i-xxx"},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId:     aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
		},
		{
			description: "should return error when can't find any instances",
			input:       []string{"i-zzz"},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedError: fmt.Errorf("failed to retrieve instances"),
		},
		{
			description: "should detach sg-xxx from multiple instances",
			input:       []string{"i-xxx", "i-yyy"},
			sg:          "sg-xxx",
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
						{
							GroupId: aws.String("sg-xxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
				{
					InstanceId: aws.String("i-yyy"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
						{
							GroupId: aws.String("sg-xxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy"},
				"i-yyy": {"sg-yyy"},
			},
		},
		{
			description: "should do nothing when sg is empty",
			input:       []string{"i-xxx"},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-yyy"),
						},
						{
							GroupId: aws.String("sg-xxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expected: map[string][]string{
				"i-xxx": {"sg-yyy", "sg-xxx"},
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *ec2.DescribeInstancesInput, opts []func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
				var instances []ec2types.Instance
				for _, instanceID := range tc.input {
					for _, instance := range tc.instances {
						if *instance.InstanceId == instanceID {
							instances = append(instances, instance)
						}
					}
				}
				return &ec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: instances,
						},
					},
				}, nil
			}

			sgsAttached := map[string][]string{}
			for _, instance := range tc.instances {
				for _, sg := range instance.SecurityGroups {
					sgsAttached[*instance.InstanceId] = append(sgsAttached[*instance.InstanceId], *sg.GroupId)
				}
			}

			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *ec2.ModifyInstanceAttributeInput, opts []func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error) {
				sgsAttached[*params.InstanceId] = params.Groups
				return &ec2.ModifyInstanceAttributeOutput{}, nil
			}

			err := DetachSecurityGroupFromInstances(ctx, fakeEC2Client, tc.input, tc.sg)
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				for instanceId, sgs := range tc.expected {
					sort.Strings(sgs)
					sort.Strings(sgsAttached[instanceId])
					g.Expect(sgsAttached[instanceId]).To(BeEquivalentTo(sgs))
				}
			}
		})
	}
}

func TestGetLaunchTemplateFromInstanceGroup(t *testing.T) {
	testCases := []struct {
		description                 string
		expectedError               error
		errorDescribeLaunchTemplate bool
		emptyDescribeLaunchTemplate bool
	}{
		{
			description: "should return launch template from instance group",
		},
		{
			description:                 "should return error when failt to get launch template",
			expectedError:               errors.New("failed to retrieve launch template from instance group"),
			errorDescribeLaunchTemplate: true,
		},
		{
			description:                 "should return error when launch template is empty",
			expectedError:               errors.New("failed to get launch template"),
			emptyDescribeLaunchTemplate: true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fake.MockEC2Client{}
			if tc.errorDescribeLaunchTemplate {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
					return nil, errors.New("failed to retrieve launch template from instance group")
				}
			} else if tc.emptyDescribeLaunchTemplate {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
					return &ec2.DescribeLaunchTemplatesOutput{}, nil
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *ec2.DescribeLaunchTemplatesInput, optFns []func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
					return &ec2.DescribeLaunchTemplatesOutput{
						LaunchTemplates: []ec2types.LaunchTemplate{
							{
								LaunchTemplateId:    aws.String("lt-xxx"),
								LaunchTemplateName:  aws.String("lt-name"),
								LatestVersionNumber: aws.Int64(1),
								Tags: []ec2types.Tag{
									{
										Key:   aws.String("tag:KubernetesCluster"),
										Value: aws.String("cluster-xxx"),
									},
									{
										Key:   aws.String("tag:Name"),
										Value: aws.String("lt-name"),
									},
								},
							},
						},
					}, nil
				}
			}

			_, err := GetLaunchTemplateFromInstanceGroup(ctx, fakeEC2Client, "cluster-xxx", "lt-name")
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}
