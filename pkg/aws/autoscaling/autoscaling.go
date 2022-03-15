package autoscaling

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/pkg/errors"
)

type AutoScalingClient interface {
	DescribeAutoScalingGroups(ctx context.Context, params *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

func NewAutoScalingClient(cfg aws.Config) AutoScalingClient {
	return autoscaling.NewFromConfig(cfg)
}

func GetAutoScalingGroupByName(ctx context.Context, autoScalingClient AutoScalingClient, asgName string) (*autoscalingtypes.AutoScalingGroup, error) {
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{
			asgName,
		},
	}

	result, err := autoScalingClient.DescribeAutoScalingGroups(ctx, input)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to retrieve AutoScalingGroup %s", asgName))
	}

	if len(result.AutoScalingGroups) == 0 {
		return nil, errors.Wrap(errors.Errorf("ASG Not Found"), fmt.Sprintf("failed to retrieve AutoScalingGroup %s", asgName))
	}

	return &result.AutoScalingGroups[0], nil

}
