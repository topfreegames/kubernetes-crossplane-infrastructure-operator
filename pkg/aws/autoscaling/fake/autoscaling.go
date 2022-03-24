package fake

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
)

type MockAutoScalingClient struct {
	MockDescribeAutoScalingGroups func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	MockUpdateAutoScalingGroup    func(ctx context.Context, params *autoscaling.UpdateAutoScalingGroupInput, optFns []func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error)
}

func (m *MockAutoScalingClient) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return m.MockDescribeAutoScalingGroups(ctx, params, optFns)
}

func (m *MockAutoScalingClient) UpdateAutoScalingGroup(ctx context.Context, params *autoscaling.UpdateAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.UpdateAutoScalingGroupOutput, error) {
	return m.MockUpdateAutoScalingGroup(ctx, params, optFns)
}
