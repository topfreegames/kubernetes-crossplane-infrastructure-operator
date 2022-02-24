package kops

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kopsapi "k8s.io/kops/pkg/apis/kops"
)

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
