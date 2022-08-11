package kops

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kopsapi "k8s.io/kops/pkg/apis/kops"
)

func TestGetSubnetFromKopsControlPlane(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should succeed getting subnet from KCP",
			"input": &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-control-plane",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					KopsClusterSpec: kopsapi.ClusterSpec{
						Subnets: []kopsapi.ClusterSubnetSpec{
							{
								Name: "test-subnet",
								CIDR: "0.0.0.0/26",
								Zone: "us-east-1d",
							},
						},
					},
				},
			},
			"expectedError": false,
		},
		{
			"description": "should fail when missing subnets",
			"input": &kcontrolplanev1alpha1.KopsControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-control-plane",
				},
				Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
					KopsClusterSpec: kopsapi.ClusterSpec{},
				},
			},
			"expectedError": true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)
	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			subnet, err := GetSubnetFromKopsControlPlane(tc["input"].(*kcontrolplanev1alpha1.KopsControlPlane))
			if !tc["expectedError"].(bool) {
				g.Expect(err).To(BeNil())
				g.Expect(subnet).ToNot(BeNil())
			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestGetRegionFromKopsSubnet(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should succeed using subnet with zone",
			"input": kopsapi.ClusterSubnetSpec{
				Zone: "us-east-1d",
			},
			"expected":      "us-east-1",
			"expectedError": false,
		},
		{
			"description": "should succeed using subnet with region",
			"input": kopsapi.ClusterSubnetSpec{
				Region: "us-east-1",
			},
			"expected":      "us-east-1",
			"expectedError": false,
		},
		{
			"description":   "should fail using subnet empty",
			"input":         kopsapi.ClusterSubnetSpec{},
			"expectedError": true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)
	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			region, err := GetRegionFromKopsSubnet(tc["input"].(kopsapi.ClusterSubnetSpec))
			if !tc["expectedError"].(bool) {
				g.Expect(region).ToNot(BeNil())
				g.Expect(err).To(BeNil())
				g.Expect(*region).To(Equal(tc["expected"].(string)))
			} else {
				g.Expect(err).ToNot(BeNil())
			}
		})
	}
}

func TestGetAutoScalingGroupNameFromKopsMachinePool(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description": "should return the correct asgName",
			"input": kinfrastructurev1alpha1.KopsMachinePool{
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "nodes-a",
						},
					},
				},
			},
			"expected":      "nodes-a.test-cluster",
			"expectedError": false,
		},
		{
			"description": "should fail when missing nodeLabel annotation",
			"input": kinfrastructurev1alpha1.KopsMachinePool{
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{},
					},
				},
			},
			"expected":             "nodes-a.test-cluster",
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve igName",
		},
		{
			"description": "should fail when missing clusterName",
			"input": kinfrastructurev1alpha1.KopsMachinePool{
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "nodes-a",
						},
					},
				},
			},
			"expected":             "nodes-a.test-cluster",
			"expectedError":        true,
			"expectedErrorMessage": "failed to retrieve clusterName",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)
	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			asgName, err := GetAutoScalingGroupNameFromKopsMachinePool(tc["input"].(kinfrastructurev1alpha1.KopsMachinePool))
			if !tc["expectedError"].(bool) {
				g.Expect(asgName).ToNot(BeNil())
				g.Expect(err).To(BeNil())
				g.Expect(*asgName).To(Equal(tc["expected"].(string)))
			} else {
				g.Expect(err).ToNot(BeNil())
				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
			}
		})
	}
}

//func TestGetKopsMachinePoolsWithLabel(t *testing.T) {
//	testCases := []map[string]interface{}{
//		{
//			"description": "should return the correct machinepool set",
//			"input":       "cluster.x-k8s.io/cluster-name: test-cluster",
//			"expected": kinfrastructurev1alpha1.KopsMachinePool{
//				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
//					ClusterName: "test-cluster",
//					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
//						NodeLabels: map[string]string{
//							"kops.k8s.io/instance-group-name": "nodes-a",
//						},
//					},
//				},
//			},
//			"expectedError": false,
//		},
//		{
//			"description": "should fail when missing cluster name label",
//			"input": kinfrastructurev1alpha1.KopsMachinePool{
//				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
//					ClusterName: "test-cluster",
//					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
//						NodeLabels: map[string]string{},
//					},
//				},
//			},
//			"expected":             "nodes-a.test-cluster",
//			"expectedError":        true,
//			"expectedErrorMessage": "failed to retrieve igName",
//		},
//		{
//			"description": "should return an empty list when no cluster exist",
//			"input": kinfrastructurev1alpha1.KopsMachinePool{
//				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
//					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
//						NodeLabels: map[string]string{
//							"kops.k8s.io/instance-group-name": "nodes-a",
//						},
//					},
//				},
//			},
//			"expected":             "nodes-a.test-cluster",
//			"expectedError":        true,
//			"expectedErrorMessage": "failed to retrieve clusterName",
//		},
//	}
//	RegisterFailHandler(Fail)
//	g := NewWithT(t)
//	for _, tc := range testCases {
//		t.Run(tc["description"].(string), func(t *testing.T) {
//			asgName, err := GetAutoScalingGroupNameFromKopsMachinePool(tc["input"].(kinfrastructurev1alpha1.KopsMachinePool))
//			if !tc["expectedError"].(bool) {
//				g.Expect(asgName).ToNot(BeNil())
//				g.Expect(err).To(BeNil())
//				g.Expect(*asgName).To(Equal(tc["expected"].(string)))
//			} else {
//				g.Expect(err).ToNot(BeNil())
//				g.Expect(err.Error()).To(ContainSubstring(tc["expectedErrorMessage"].(string)))
//			}
//		})
//	}
//}
