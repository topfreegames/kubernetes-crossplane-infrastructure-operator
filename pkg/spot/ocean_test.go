package spot

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	fakeocean "github.com/topfreegames/provider-crossplane/pkg/spot/fake"
)

func TestListVNGsFromClusterName(t *testing.T) {
	testCases := []struct {
		description  string
		clusterName  string
		expectedVNGs []*oceanaws.LaunchSpec
	}{
		{
			description: "Should return all VNGs giving a cluster name",
			clusterName: "test",
			expectedVNGs: []*oceanaws.LaunchSpec{
				{
					ID:      aws.String("1"),
					Name:    aws.String("vng-test"),
					OceanID: aws.String("o-1"),
				},
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}

			fakeOceanClient.MockListClusters = func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test"),
						},
					},
				}, nil
			}

			fakeOceanClient.MockListLaunchSpecs = func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
				return &oceanaws.ListLaunchSpecsOutput{
					LaunchSpecs: []*oceanaws.LaunchSpec{
						{
							ID:      aws.String("1"),
							Name:    aws.String("vng-test"),
							OceanID: aws.String("o-1"),
						},
					},
				}, nil
			}
			listLaunchSpec, err := ListVNGsFromClusterName(ctx, fakeOceanClient, tc.clusterName)
			g.Expect(err).To(BeNil())
			g.Expect(tc.expectedVNGs).To(BeEquivalentTo(listLaunchSpec))
		})
	}

}
