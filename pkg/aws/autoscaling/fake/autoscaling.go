package fake

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
)

type MockAutoScalingClient struct {
	MockDescribeAutoScalingGroups func(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns []func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

func (m *MockAutoScalingClient) DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return m.MockDescribeAutoScalingGroups(ctx, params, optFns)
}
