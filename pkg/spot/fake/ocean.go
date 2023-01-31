package fake

import (
	"context"

	oceanAws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
)

type MockOceanCloudProviderAWS struct {
	MockListClusters     func(ctx context.Context, listClusterInput *oceanAws.ListClustersInput) (*oceanAws.ListClustersOutput, error)
	MockListLaunchSpecs  func(ctx context.Context, listLaunchSpecsInput *oceanAws.ListLaunchSpecsInput) (*oceanAws.ListLaunchSpecsOutput, error)
	MockUpdateLaunchSpec func(ctx context.Context, updateLaunchSpecInput *oceanAws.UpdateLaunchSpecInput) (*oceanAws.UpdateLaunchSpecOutput, error)
}

func (m *MockOceanCloudProviderAWS) ListClusters(ctx context.Context, listClusterInput *oceanAws.ListClustersInput) (*oceanAws.ListClustersOutput, error) {
	return m.MockListClusters(ctx, listClusterInput)
}

func (m *MockOceanCloudProviderAWS) ListLaunchSpecs(ctx context.Context, listLaunchSpecsInput *oceanAws.ListLaunchSpecsInput) (*oceanAws.ListLaunchSpecsOutput, error) {
	return m.MockListLaunchSpecs(ctx, listLaunchSpecsInput)
}

func (m *MockOceanCloudProviderAWS) UpdateLaunchSpec(ctx context.Context, updateLaunchSpecInput *oceanAws.UpdateLaunchSpecInput) (*oceanAws.UpdateLaunchSpecOutput, error) {
	return m.MockUpdateLaunchSpec(ctx, updateLaunchSpecInput)
}
