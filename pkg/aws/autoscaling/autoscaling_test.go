package autoscaling

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling/fake"
)

func TestGetAutoScalingGroupByName(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return a list of ASG",
			"mockDescribeAutoScalingGroups": func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &autoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("testASG"),
						},
					},
				}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should return error failing to describe ASG",
			"mockDescribeAutoScalingGroups": func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
				return nil, errors.New("failed to retrieve AutoScalingGroup testASG")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve AutoScalingGroup",
		},
		{
			"description": "should return error when result is empty",
			"mockDescribeAutoScalingGroups": func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &autoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{},
				}, errors.New("ASG Not Found")
			},
			"expectedError":        true,
			"expectedErrorMessage": "ASG Not Found",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			ctx := context.TODO()
			fakeASGClient := &fake.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = tc["mockDescribeAutoScalingGroups"].(func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error))
			asg, err := GetAutoScalingGroupByName(ctx, fakeASGClient, "testASG")

			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(asg).ToNot(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}

func TestUpdateAutoScalingGroupLaunchTemplate(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should update LT from ASG",
			"mockUpdateAutoScalingGroup": func(ctx context.Context, params *autoscaling.UpdateAutoScalingGroupInput, optFns []func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error) {
				return &autoscaling.UpdateAutoScalingGroupOutput{}, nil
			},
			"expectedError": false,
		},
		{
			"description": "should fail with error in UpdateAutoScalingGroup",
			"mockUpdateAutoScalingGroup": func(ctx context.Context, params *autoscaling.UpdateAutoScalingGroupInput, optFns []func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error) {
				return nil, errors.New("some error")
			},
			"expectedError":        true,
			"expectedErrorMessage": "failed to update ASG",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)
	ctx := context.TODO()

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {

			fakeASGClient := &fake.MockAutoScalingClient{}
			fakeASGClient.MockUpdateAutoScalingGroup = tc["mockUpdateAutoScalingGroup"].(func(ctx context.Context, params *autoscaling.UpdateAutoScalingGroupInput, optFns []func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error))

			ltVersion := ec2types.LaunchTemplateVersion{
				VersionNumber:    aws.Int64(1),
				LaunchTemplateId: aws.String("lt-xxxx"),
			}

			result, err := UpdateAutoScalingGroupLaunchTemplate(ctx, fakeASGClient, ltVersion, "nodes.cluster-name")
			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(result).ToNot(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}
