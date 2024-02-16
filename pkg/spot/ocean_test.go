package spot

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	fakeocean "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot/fake"
)

func TestListVNGsFromClusterName(t *testing.T) {
	testCases := []struct {
		description                string
		clusterName                string
		mockListClusters           func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error)
		mockListLaunchSpecs        func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error)
		expectedVNGs               []*oceanaws.LaunchSpec
		expectFailErrorClusters    bool
		expectFailErrorLaunchSpecs bool
	}{
		{
			description: "should return all VNGs giving a cluster name",
			clusterName: "test",
			mockListClusters: func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test"),
						},
					},
				}, nil
			},
			mockListLaunchSpecs: func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return &oceanaws.ListLaunchSpecsOutput{
					LaunchSpecs: []*oceanaws.LaunchSpec{
						{
							ID:      aws.String("1"),
							Name:    aws.String("vng-test"),
							OceanID: aws.String("o-1"),
						},
					},
				}, nil
			},
			expectedVNGs: []*oceanaws.LaunchSpec{
				{
					ID:      aws.String("1"),
					Name:    aws.String("vng-test"),
					OceanID: aws.String("o-1"),
				},
			},
		},
		{
			description: "should fail if can't retrieve clusters",
			mockListClusters: func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return nil, fmt.Errorf("error")
			},
			expectFailErrorClusters: true,
		},
		{
			description: "should fail if can't retrieve lauchspecs",
			clusterName: "test",
			mockListClusters: func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test"),
						},
					},
				}, nil
			},
			mockListLaunchSpecs: func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return nil, fmt.Errorf("error")
			},
			expectFailErrorLaunchSpecs: true,
		},
		{
			description: "should fail if cluster is not in the clusterList",
			clusterName: "test2",
			mockListClusters: func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test"),
						},
					},
				}, nil
			},
			mockListLaunchSpecs: func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return nil, nil
			},
			expectFailErrorClusters: true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}

			fakeOceanClient.MockListClusters = tc.mockListClusters
			fakeOceanClient.MockListLaunchSpecs = tc.mockListLaunchSpecs

			listLaunchSpec, err := ListVNGsFromClusterName(ctx, fakeOceanClient, tc.clusterName)
			if tc.expectFailErrorClusters || tc.expectFailErrorLaunchSpecs {
				g.Expect(err).To(Not(BeNil()))
				g.Expect(listLaunchSpec).To(BeNil())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(tc.expectedVNGs).To(BeEquivalentTo(listLaunchSpec))
			}
		})
	}

}
