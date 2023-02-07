package spot

import (
	"context"
	"fmt"

	"github.com/spotinst/spotinst-sdk-go/service/ocean"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	"github.com/spotinst/spotinst-sdk-go/spotinst/session"
)

type OceanClient interface {
	ListClusters(context.Context, *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error)
	ListLaunchSpecs(context.Context, *oceanaws.ListLaunchSpecsInput) (*oceanaws.ListLaunchSpecsOutput, error)
	UpdateLaunchSpec(context.Context, *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
}

func NewOceanCloudProviderAWS() OceanClient {
	sess := session.New()
	svc := ocean.New(sess)
	return svc.CloudProviderAWS()
}

func ListVNGsFromClusterName(ctx context.Context, oceanClient OceanClient, clusterName string) ([]*oceanaws.LaunchSpec, error) {
	listClusters, err := oceanClient.ListClusters(ctx, &oceanaws.ListClustersInput{})
	if err != nil {
		return nil, fmt.Errorf("error retrieving ocean clusters: %w", err)
	}

	for _, cluster := range listClusters.Clusters {
		if *cluster.ControllerClusterID == clusterName {
			listLaunchSpecs, err := oceanClient.ListLaunchSpecs(ctx, &oceanaws.ListLaunchSpecsInput{
				OceanID: cluster.ID,
			})
			if err != nil {
				return nil, fmt.Errorf("error retrieving ocean cluster launch specs: %w", err)
			}
			return listLaunchSpecs.LaunchSpecs, nil
		}
	}

	return nil, fmt.Errorf("error retrieving vng's from cluster: %s", clusterName)
}
