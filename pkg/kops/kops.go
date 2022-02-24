package kops

import (
	kopsapi "k8s.io/kops/pkg/apis/kops"

	"github.com/pkg/errors"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
)

func GetSubnetFromKopsControlPlane(kcp *kcontrolplanev1alpha1.KopsControlPlane) (*kopsapi.ClusterSubnetSpec, error) {
	if kcp.Spec.KopsClusterSpec.Subnets == nil {
		return nil, errors.Wrap(errors.Errorf("SubnetNotFound"), "subnet not found in KopsControlPlane")
	}
	subnet := kcp.Spec.KopsClusterSpec.Subnets[0]
	return &subnet, nil
}

func GetRegionFromKopsSubnet(subnet kopsapi.ClusterSubnetSpec) (*string, error) {
	if subnet.Region != "" {
		return &subnet.Region, nil
	}

	if subnet.Zone != "" {
		zone := subnet.Zone
		region := zone[:len(zone)-1]
		return &region, nil
	}

	return nil, errors.Wrap(errors.Errorf("RegionNotFound"), "couldn't get region from KopsControlPlane")
}
