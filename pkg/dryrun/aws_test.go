package dryrun

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	. "github.com/onsi/gomega"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"

	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
	fakeocean "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot/fake"
)

func newMockEC2Client(t *testing.T, latestVersion int64) *fakeec2.MockEC2Client {
	return &fakeec2.MockEC2Client{
		MockDescribeLaunchTemplateVersions: func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
			return &awsec2.DescribeLaunchTemplateVersionsOutput{
				LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
					{
						LaunchTemplateId:   aws.String("lt-xxxx"),
						LaunchTemplateName: aws.String("lt-name"),
						VersionNumber:      aws.Int64(latestVersion),
						LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
							NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
								{Groups: []string{"sg-old"}},
							},
						},
					},
				},
			}, nil
		},
		MockDescribeSecurityGroups: func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
			return &awsec2.DescribeSecurityGroupsOutput{}, nil
		},
		MockDescribeInstances: func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
			return &awsec2.DescribeInstancesOutput{
				Reservations: []ec2types.Reservation{
					{
						Instances: []ec2types.Instance{
							{
								InstanceId:     aws.String("i-xxxx"),
								State:          &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
								SecurityGroups: []ec2types.GroupIdentifier{{GroupId: aws.String("sg-old")}},
							},
						},
					},
				},
			}, nil
		},
		MockCreateLaunchTemplateVersion: func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
			t.Fatal("CreateLaunchTemplateVersion reached the real client")
			return nil, nil
		},
		MockModifyInstanceAttribute: func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
			t.Fatal("ModifyInstanceAttribute reached the real client")
			return nil, nil
		},
	}
}

func TestEC2ClientCreateLaunchTemplateVersion(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	c := EC2ClientFactory(func(cfg aws.Config) ec2.EC2Client {
		return newMockEC2Client(t, 7)
	})(aws.Config{})

	output, err := c.CreateLaunchTemplateVersion(ctx, &awsec2.CreateLaunchTemplateVersionInput{
		LaunchTemplateId: aws.String("lt-xxxx"),
		SourceVersion:    aws.String("$Latest"),
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(*output.LaunchTemplateVersion.LaunchTemplateId).To(Equal("lt-xxxx"))
	g.Expect(*output.LaunchTemplateVersion.VersionNumber).To(Equal(int64(8)))
}

func TestEC2ClientCreateLaunchTemplateVersionDescribeError(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	mock := newMockEC2Client(t, 7)
	mock.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
		return nil, errors.New("describe failed")
	}
	c := &EC2Client{real: mock}

	_, err := c.CreateLaunchTemplateVersion(ctx, &awsec2.CreateLaunchTemplateVersionInput{LaunchTemplateId: aws.String("lt-xxxx")})
	g.Expect(err).To(MatchError("describe failed"))
}

func TestEC2ClientAttachSecurityGroupToLaunchTemplate(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	c := &EC2Client{real: newMockEC2Client(t, 7)}

	ltVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, c, "lt-xxxx")
	g.Expect(err).NotTo(HaveOccurred())

	output, err := ec2.AttachSecurityGroupToLaunchTemplate(ctx, c, "sg-new", ltVersion)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(*output.LaunchTemplateVersion.LaunchTemplateId).To(Equal("lt-xxxx"))
	g.Expect(*output.LaunchTemplateVersion.VersionNumber).To(Equal(int64(8)))
}

func TestEC2ClientModifyInstanceAttribute(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	c := &EC2Client{real: newMockEC2Client(t, 7)}

	err := ec2.AttachSecurityGroupToInstances(ctx, c, []string{"i-xxxx"}, "sg-new")
	g.Expect(err).NotTo(HaveOccurred())
}

func TestEC2ClientReadsGoToRealClient(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	c := &EC2Client{real: newMockEC2Client(t, 7)}

	output, err := c.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(*output.Reservations[0].Instances[0].InstanceId).To(Equal("i-xxxx"))
}

func TestAutoScalingClient(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	mock := &fakeasg.MockAutoScalingClient{
		MockDescribeAutoScalingGroups: func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
			return &awsautoscaling.DescribeAutoScalingGroupsOutput{
				AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{AutoScalingGroupName: aws.String("asg")}},
			}, nil
		},
		MockUpdateAutoScalingGroup: func(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
			t.Fatal("UpdateAutoScalingGroup reached the real client")
			return nil, nil
		},
	}
	c := AutoScalingClientFactory(func(cfg aws.Config) autoscaling.AutoScalingClient {
		return mock
	})(aws.Config{})

	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, c, "asg")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(*asg.AutoScalingGroupName).To(Equal("asg"))

	output, err := autoscaling.UpdateAutoScalingGroupLaunchTemplate(ctx, c, "lt-xxxx", "8", "asg")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).NotTo(BeNil())
}

func TestOceanClient(t *testing.T) {
	g := NewWithT(t)
	ctx := context.TODO()

	mock := &fakeocean.MockOceanCloudProviderAWS{
		MockListClusters: func(ctx context.Context, input *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
			return &oceanaws.ListClustersOutput{Clusters: []*oceanaws.Cluster{{ID: aws.String("o-xxxx")}}}, nil
		},
		MockUpdateLaunchSpec: func(ctx context.Context, input *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
			t.Fatal("UpdateLaunchSpec reached the real client")
			return nil, nil
		},
	}
	c := OceanClientFactory(func() spot.OceanClient {
		return mock
	})()

	clusters, err := c.ListClusters(ctx, &oceanaws.ListClustersInput{})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(*clusters.Clusters[0].ID).To(Equal("o-xxxx"))

	launchSpec := &oceanaws.LaunchSpec{ID: aws.String("ols-xxxx"), SecurityGroupIDs: []string{"sg-new"}}
	output, err := c.UpdateLaunchSpec(ctx, &oceanaws.UpdateLaunchSpecInput{LaunchSpec: launchSpec})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(output).NotTo(BeNil())
}
