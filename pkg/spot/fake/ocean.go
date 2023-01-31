package fake

import (
	"context"

	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
)

type MockOceanCloudProviderAWS struct {
	MockListClusters     func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error)
	MockListLaunchSpecs  func(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error)
	MockUpdateLaunchSpec func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
}

func (m *MockOceanCloudProviderAWS) ListClusters(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
	return m.MockListClusters(ctx, listClusterInput)
}

func (m *MockOceanCloudProviderAWS) ListLaunchSpecs(ctx context.Context, listLaunchSpecsInput *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error) {
	return m.MockListLaunchSpecs(ctx, listLaunchSpecsInput)
}

func (m *MockOceanCloudProviderAWS) UpdateLaunchSpec(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
	return m.MockUpdateLaunchSpec(ctx, updateLaunchSpecInput)
}
