package kops

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/labels"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/pkg/errors"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
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

// GetKopsMachinePoolsWithLabel retrieve all KopsMachinePool with the label in format 'key: value'
func GetKopsMachinePoolsWithLabel(ctx context.Context, c client.Client, key, value string) ([]kinfrastructurev1alpha1.KopsMachinePool, error) {
	var kmps []kinfrastructurev1alpha1.KopsMachinePool

	req, err := labels.NewRequirement(key, "==", []string{value})
	if err != nil {
		return kmps, err
	}

	selector := labels.NewSelector()
	selector = selector.Add(*req)

	kmpsList := &kinfrastructurev1alpha1.KopsMachinePoolList{}

	err = c.List(ctx, kmpsList, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		return kmps, err
	}
	return kmpsList.Items, nil
}

func GetAutoScalingGroupNameFromKopsMachinePool(kmp kinfrastructurev1alpha1.KopsMachinePool) (*string, error) {
	if _, ok := kmp.Spec.KopsInstanceGroupSpec.NodeLabels["kops.k8s.io/instance-group-name"]; !ok {
		return nil, fmt.Errorf("failed to retrieve igName from KopsMachinePool %s", kmp.GetName())
	}

	if kmp.Spec.ClusterName == "" {
		return nil, fmt.Errorf("failed to retrieve clusterName from KopsMachinePool %s", kmp.GetName())
	}

	asgName := fmt.Sprintf("%s.%s", kmp.Spec.KopsInstanceGroupSpec.NodeLabels["kops.k8s.io/instance-group-name"], kmp.Spec.ClusterName)

	return &asgName, nil
}
