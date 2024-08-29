/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sgcontroller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	karpenterv1alpha5 "github.com/aws/karpenter-core/pkg/apis/v1alpha5"

	karpenterv1beta1 "github.com/aws/karpenter-core/pkg/apis/v1beta1"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsautoscaling "github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/hashicorp/go-multierror"
	dto "github.com/prometheus/client_model/go"
	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/clustermesh.infrastructure/v1alpha1"
	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	custommetrics "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/metrics"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	fakeasg "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	fakeec2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2/fake"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"
	fakeocean "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot/fake"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/util/pkg/vfs"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	sg = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsMachinePool",
					Name:       "test-kops-machine-pool",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	anotherSg = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-another-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsMachinePool",
					Name:       "test-kops-machine-pool",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	sgEmpty = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules:      []securitygroupv1alpha2.IngressRule{},
			InfrastructureRef: []*corev1.ObjectReference{},
		},
	}

	sgKCP = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsControlPlane",
					Name:       "test-cluster",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	anotherSgKCP = &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-another-security-group",
		},
		Spec: securitygroupv1alpha2.SecurityGroupSpec{
			IngressRules: []securitygroupv1alpha2.IngressRule{
				{
					IPProtocol: "TCP",
					FromPort:   40000,
					ToPort:     60000,
					AllowedCIDRBlocks: []string{
						"0.0.0.0/0",
					},
				},
			},
			InfrastructureRef: []*corev1.ObjectReference{
				{
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsControlPlane",
					Name:       "test-cluster",
					Namespace:  metav1.NamespaceDefault,
				},
			},
		},
	}

	csg = &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-security-group",
		},
		Status: crossec2v1beta1.SecurityGroupStatus{
			ResourceStatus: crossplanev1.ResourceStatus{
				ConditionedStatus: crossplanev1.ConditionedStatus{
					Conditions: []crossplanev1.Condition{
						{
							Type:   "Ready",
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			AtProvider: crossec2v1beta1.SecurityGroupObservation{
				SecurityGroupID: "sg-1",
			},
		},
	}

	anotherCsg = &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-another-security-group",
		},
		Status: crossec2v1beta1.SecurityGroupStatus{
			ResourceStatus: crossplanev1.ResourceStatus{
				ConditionedStatus: crossplanev1.ConditionedStatus{
					Conditions: []crossplanev1.Condition{
						{
							Type:   "Ready",
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			AtProvider: crossec2v1beta1.SecurityGroupObservation{
				SecurityGroupID: "sg-2",
			},
		},
	}

	kmp = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
		},
	}

	kmpWithFinalizer = func(name string, kcpName string, finalizers []string) *kinfrastructurev1alpha1.KopsMachinePool {
		return &kinfrastructurev1alpha1.KopsMachinePool{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  metav1.NamespaceDefault,
				Name:       name,
				Finalizers: finalizers,
				Labels: map[string]string{
					"cluster.x-k8s.io/cluster-name": kcpName,
				},
			},
			Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
				ClusterName: kcpName,
				KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
					NodeLabels: map[string]string{
						"kops.k8s.io/instance-group-name": "test-ig",
						"kops.k8s.io/instance-group-role": "Node",
					},
				},
			},
		}
	}

	spotKMP = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
			SpotInstOptions: map[string]string{
				"spot": "true",
			},
		},
	}

	karpenterKMPProvisioner = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KarpenterProvisioners: []karpenterv1alpha5.Provisioner{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-provisioner",
					},
				},
			},
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				Manager: "Karpenter",
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
		},
	}

	karpenterKMPNodePool = &kinfrastructurev1alpha1.KopsMachinePool{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-kops-machine-pool",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test-cluster",
			},
		},
		Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
			ClusterName: "test-cluster",
			KarpenterNodePools: []karpenterv1beta1.NodePool{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-node-pool",
					},
				},
			},
			KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
				Manager: "Karpenter",
				NodeLabels: map[string]string{
					"kops.k8s.io/instance-group-name": "test-ig",
					"kops.k8s.io/instance-group-role": "Node",
				},
			},
		},
	}

	cluster = &clusterv1beta1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
			Labels: map[string]string{
				"environment": "testing",
			},
		},
		Spec: clusterv1beta1.ClusterSpec{
			ControlPlaneRef: &corev1.ObjectReference{
				APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
				Kind:       "KopsControlPlane",
				Name:       "test-kops-control-plane",
				Namespace:  metav1.NamespaceDefault,
			},
		},
	}

	kcp = &kcontrolplanev1alpha1.KopsControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
		Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
			KopsClusterSpec: kopsapi.ClusterSpec{
				Networking: kopsapi.NetworkingSpec{
					Subnets: []kopsapi.ClusterSubnetSpec{
						{
							Name: "test-subnet",
							CIDR: "0.0.0.0/26",
							Zone: "us-east-1d",
						},
					},
					NetworkCIDR: "0.0.0.0/24",
				},
			},
			IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
				Name:      "default",
				Namespace: "kubernetes-kops-operator-system",
			},
		},
	}

	kcpWithFinalizer = func(name string, finalizers []string) *kcontrolplanev1alpha1.KopsControlPlane {
		return &kcontrolplanev1alpha1.KopsControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  metav1.NamespaceDefault,
				Name:       name,
				Finalizers: finalizers,
			},
			Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
				KopsClusterSpec: kopsapi.ClusterSpec{
					Networking: kopsapi.NetworkingSpec{
						Subnets: []kopsapi.ClusterSubnetSpec{
							{
								Name: "test-subnet",
								CIDR: "0.0.0.0/26",
								Zone: "us-east-1d",
							},
						},
						NetworkCIDR: "0.0.0.0/24",
					},
				},
				IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
					Name:      "default",
					Namespace: "kubernetes-kops-operator-system",
				},
			},
		}
	}

	kcpWithIdentityRef = &kcontrolplanev1alpha1.KopsControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "test-cluster",
		},
		Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
			KopsClusterSpec: kopsapi.ClusterSpec{
				Networking: kopsapi.NetworkingSpec{
					Subnets: []kopsapi.ClusterSubnetSpec{
						{
							Name: "test-subnet",
							CIDR: "0.0.0.0/26",
							Zone: "us-east-1d",
						},
					},
				},
			},
			IdentityRef: kcontrolplanev1alpha1.IdentityRefSpec{
				Name:      "test-secret",
				Namespace: "kubernetes-kops-operator-system",
			},
		},
	}

	secretForIdentityRef = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kubernetes-kops-operator-system",
			Name:      "test-secret",
		},
		Data: map[string][]byte{
			"AccessKeyID":     []byte("AK"),
			"SecretAccessKey": []byte("SAK"),
		},
	}

	defaultSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kubernetes-kops-operator-system",
			Name:      "default",
		},
		Data: map[string][]byte{
			"AccessKeyID":     []byte("AK"),
			"SecretAccessKey": []byte("SAK"),
		},
	}
)

type ReferencedPool struct {
	Namespace     string
	Name          string
	Kind          string
	FinalizerName string
	Expected      bool
}

type sgToReconcile struct {
	Name             string
	ExpectedDeletion bool
}

func TestSecurityGroupReconciler(t *testing.T) {

	testCases := []struct {
		description               string
		k8sObjects                []client.Object
		errorExpected             error
		sgsToReconcile            []sgToReconcile
		expectedProviderConfigRef *string
		FinalizersAt              []*ReferencedPool
		expectedStatus            *securitygroupv1alpha2.SecurityGroupStatus
	}{
		{
			description: "should fail without InfrastructureRef defined",
			k8sObjects: []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{{Name: "test-security-group", ExpectedDeletion: false}},
			errorExpected:  errors.New("infrastructureRef not supported"),
		},
		{
			description: "should create a SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sgKCP.ObjectMeta.Name, ExpectedDeletion: false}},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kcp.ObjectMeta.Name,
					Namespace:     kcp.ObjectMeta.Namespace,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should create a SecurityGroup with KopsMachinePool infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg, csg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should create two SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, anotherSgKCP, csg, anotherCsg,
			},
			sgsToReconcile: []sgToReconcile{
				{
					Name:             sgKCP.ObjectMeta.Name,
					ExpectedDeletion: false,
				},
				{
					Name:             anotherSgKCP.ObjectMeta.Name,
					ExpectedDeletion: false,
				},
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kcp.ObjectMeta.Name,
					Namespace:     kcp.ObjectMeta.Namespace,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          kcp.ObjectMeta.Name,
					Namespace:     kcp.ObjectMeta.Namespace,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should keep only existing sg finalizers in kmp",
			k8sObjects: []client.Object{
				cluster, defaultSecret, csg, anotherCsg, anotherSg, kcp,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster",
					[]string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID), getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID)}),
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-security-group",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
						Finalizers:        []string{"securitygroup.wildlife.infrastructure.io"},
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{
				{
					Name:             "test-security-group",
					ExpectedDeletion: true,
				},
				{
					Name:             anotherSg.ObjectMeta.Name,
					ExpectedDeletion: false,
				},
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should keep only existing sg finalizers in kcp and related kmps",
			k8sObjects: []client.Object{
				cluster, defaultSecret, csg, anotherCsg, anotherSgKCP,
				kcpWithFinalizer("test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID)}),
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID)}),
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-security-group",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
						Finalizers:        []string{"securitygroup.wildlife.infrastructure.io"},
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsControlPlane",
								Name:       "test-cluster",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{
				{
					Name:             "test-security-group",
					ExpectedDeletion: true,
				},
				{
					Name:             anotherSgKCP.ObjectMeta.Name,
					ExpectedDeletion: false,
				},
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
				{
					Name:          "test-cluster",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(anotherCsg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should reconcile sg without changes in kmp",
			k8sObjects: []client.Object{
				cluster, defaultSecret, csg, sg, kcp,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sgsToReconcile: []sgToReconcile{
				{
					Name:             sg.ObjectMeta.Name,
					ExpectedDeletion: false,
				},
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should reconcile sg without changes in kcp",
			k8sObjects: []client.Object{
				cluster, defaultSecret, csg, sgKCP,
				kcpWithFinalizer("test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sgsToReconcile: []sgToReconcile{
				{
					Name:             string(sgKCP.ObjectMeta.Name),
					ExpectedDeletion: false,
				},
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should update a SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sgKCP.ObjectMeta.Name, ExpectedDeletion: false}},
		},
		{
			description: "should create a Crossplane SecurityGroup with KopsControlPlane infrastructureRef and return that SecurityGroups isn't available",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP,
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
			errorExpected:  ErrSecurityGroupNotAvailable,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName("sg-1"),
					Expected:      false,
				},
				{
					Name:          kcp.ObjectMeta.Name,
					Namespace:     kcp.ObjectMeta.Namespace,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName("sg-1"),
					Expected:      false,
				},
			},
		},
		{
			description: "should update an available Crossplane SecurityGroup with KopsControlPlane infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sgKCP.ObjectMeta.Name, ExpectedDeletion: false}},
		},
		{
			description: "should create a Crossplane SecurityGroup with KopsMachinePool infrastructureRef and return that SecurityGroups isn't available",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
			errorExpected:  ErrSecurityGroupNotAvailable,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName("sg-1"),
					Expected:      false,
				},
			},
		},
		{
			description: "should update an available Crossplane SecurityGroup with KopsMachinePool infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sg, csg,
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
		},
		{
			description: "should fail with InfrastructureRef Kind different from KopsMachinePool and KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, kcp,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "MachinePool",
								Name:       "test-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{{Name: "test-security-group", ExpectedDeletion: false}},
			errorExpected:  errors.New("infrastructureRef not supported"),
		},
		{
			description: "should remove SecurityGroup with DeletionTimestamp",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-security-group",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
						Finalizers:        []string{"securitygroup.wildlife.infrastructure.io"},
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{{Name: "test-security-group", ExpectedDeletion: true}},
		},
		{
			description: "should create a SecurityGroup with the same providerConfigName",
			k8sObjects: []client.Object{
				kmp, cluster, kcpWithIdentityRef, secretForIdentityRef, sgKCP, csg,
			},
			sgsToReconcile:            []sgToReconcile{{Name: sgKCP.ObjectMeta.Name, ExpectedDeletion: false}},
			expectedProviderConfigRef: &kcpWithIdentityRef.Spec.IdentityRef.Name,
		},
		{
			description: "should fail when not finding KopsMachinePool",
			k8sObjects: []client.Object{
				cluster, kcp, sg, defaultSecret,
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
			errorExpected:  apierrors.NewNotFound(schema.GroupResource{Group: "infrastructure.cluster.x-k8s.io", Resource: "kopsmachinepools"}, kmp.Name),
		},
		{
			description: "should fail when not finding KopsControlPlane",
			k8sObjects: []client.Object{
				kmp, cluster, sgKCP, defaultSecret,
			},
			sgsToReconcile: []sgToReconcile{{Name: sgKCP.ObjectMeta.Name, ExpectedDeletion: false}},
			errorExpected:  apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, kcp.Name),
		},
		{
			description: "should update finalizers to match the spec",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, csg,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
					Status: securitygroupv1alpha2.SecurityGroupStatus{
						Ready: true,
						AppliedInfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-another-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-another-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
						Finalizers: []string{
							getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
					},
				},
			},
			sgsToReconcile: []sgToReconcile{{Name: sg.ObjectMeta.Name, ExpectedDeletion: false}},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-another-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						LaunchTemplateId: params.LaunchTemplateId,
						VersionNumber:    aws.Int64(2),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}
			fakeASGClient.MockUpdateAutoScalingGroup = func(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
				Expect(*params.LaunchTemplate.Version).To(BeEquivalentTo("2"))
				return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
			}
			reconciler := &SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(5),
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			for _, sg := range tc.sgsToReconcile {
				_, err = reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Name: sg.Name,
					},
				})

				if tc.errorExpected == nil {
					crosssg := &crossec2v1beta1.SecurityGroup{}
					key := client.ObjectKey{
						Name: sg.Name,
					}
					err = fakeClient.Get(ctx, key, crosssg)
					if !sg.ExpectedDeletion {
						g.Expect(err).To(BeNil())
						g.Expect(crosssg).NotTo(BeNil())
					} else {
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
					}

					if tc.expectedProviderConfigRef != nil {
						g.Expect(crosssg.Spec.ProviderConfigReference.Name).To(Equal(*tc.expectedProviderConfigRef))
					}

				} else {
					g.Expect(err).To(MatchError(tc.errorExpected))
				}
			}

			for _, expectedFinalizerAt := range tc.FinalizersAt {
				switch expectedFinalizerAt.Kind {
				case "KopsMachinePool":
					kmp := kinfrastructurev1alpha1.KopsMachinePool{}
					key := client.ObjectKey{
						Name:      expectedFinalizerAt.Name,
						Namespace: expectedFinalizerAt.Namespace,
					}
					_ = fakeClient.Get(ctx, key, &kmp)
					if expectedFinalizerAt.Expected {
						g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
					} else {
						g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
					}
				case "KopsControlPlane":
					kcp := kcontrolplanev1alpha1.KopsControlPlane{}
					key := client.ObjectKey{
						Name:      expectedFinalizerAt.Name,
						Namespace: expectedFinalizerAt.Namespace,
					}
					_ = fakeClient.Get(ctx, key, &kcp)
					if expectedFinalizerAt.Expected {
						g.Expect(kcp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
					} else {
						g.Expect(kcp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
					}

					kmps, _ := kops.GetKopsMachinePoolsWithLabel(ctx, fakeClient, "cluster.x-k8s.io/cluster-name", kcp.Name)
					for _, kmp := range kmps {
						if expectedFinalizerAt.Expected {
							g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
						} else {
							g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					}
				default:
					g.Fail("Unexpected kind")
				}
			}
		})
	}
}

func TestRetrieveInfraRefInfo(t *testing.T) {

	testCases := []struct {
		description          string
		k8sObjects           []client.Object
		errorExpected        error
		targetSG             *securitygroupv1alpha2.SecurityGroup
		mockUpdateLaunchSpec func(context.Context, *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
	}{
		{
			description: "should return error when infrastructureRef empty",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sgEmpty,
			errorExpected: errors.New("no infrastructureRef found"),
		},
		{
			description: "should return error when all refs fail to get KMP",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sg,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "infrastructure.cluster.x-k8s.io", Resource: "kopsmachinepools"}, kmp.Name),
		},
		{
			description: "should return error when all refs fail to get KCP of KMP",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG:      sg,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should return nil",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: sg,
		},
		{
			description: "should return error when all refs fail to get KCP",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG:      sgKCP,
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should return error when all refs are from unknown type",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-fake-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: errors.New("infrastructureRef not supported"),
		},
		{
			description: "should not fail when one of the refs kmp does not exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should not fail when one of the refs kmp does not exists but kcp exists",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when all refs are incomplete kmp",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should not fail when one of the refs kcp does not exists but kmp exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should not fail when one of the refs kcp does not exists",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-fake",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when all refs are incomplete",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should fail when all refs are incomplete kcp",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, "test-cluster-2"),
		},
		{
			description: "should not fail when one of the refs is type unknown but kmp exists",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when one of the refs is type unknown but kmp does not have kcp",
			k8sObjects: []client.Object{
				cluster,
				kmp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
		{
			description: "should not fail when one of the refs is type unknown and kcp exist",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
		},
		{
			description: "should fail when one of the refs is type unknown but kcp does not exist",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
			},
			targetSG: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "FakeMachinePool",
							Name:       "test-mp",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			errorExpected: apierrors.NewNotFound(schema.GroupResource{Group: "controlplane.cluster.x-k8s.io", Resource: "kopscontrolplanes"}, cluster.Name),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}

			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      tc.targetSG,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.retrieveInfraRefInfo(ctx)
			if tc.errorExpected == nil {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

			} else {
				g.Expect(tc.errorExpected).To(MatchError(err))
			}
		})
	}
}

func TestAttachKopsMachinePool(t *testing.T) {

	testCases := []struct {
		description          string
		k8sObjects           []client.Object
		instances            []ec2types.Instance
		kmp                  *kinfrastructurev1alpha1.KopsMachinePool
		errorExpected        error
		mockUpdateLaunchSpec func(context.Context, *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error)
	}{
		{
			description: "should reconcile without error with spotKMP",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: spotKMP,
		},
		{
			description: "should reconcile without error with karpenter KMP Provisioner",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: karpenterKMPProvisioner,
		},
		{
			description: "should reconcile without error with karpenter KMP NodePool",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: karpenterKMPNodePool,
		},
		{
			description: "should reconcile without error with karpenter KMP NodePool",
			k8sObjects: []client.Object{
				cluster,
				kcp,
				sg,
				csg,
				defaultSecret,
			},
			kmp: karpenterKMPProvisioner,
		},
		{
			description: "should fail with wrong spotKMP clusterName",
			k8sObjects: []client.Object{
				cluster,
				sg,
				csg,
				defaultSecret,
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-cluster-2",
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
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
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster-2",
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster-2",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
						SpotInstOptions: map[string]string{
							"spot": "true",
						},
					},
				},
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster-2",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster-2",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
							"kops.k8s.io/instance-group-role": "Node",
						},
					},
					SpotInstOptions: map[string]string{
						"spot": "true",
					},
				},
			},
			errorExpected: errors.New("error retrieving vng's from cluster: test-cluster-2"),
		},
		{
			description: "should fail if cant match vng with kmp name",
			k8sObjects: []client.Object{
				cluster,
				defaultSecret,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool-2",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
				csg,
				&kcontrolplanev1alpha1.KopsControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-control-plane",
					},
					Spec: kcontrolplanev1alpha1.KopsControlPlaneSpec{
						KopsClusterSpec: kopsapi.ClusterSpec{
							Networking: kopsapi.NetworkingSpec{
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
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-machine-pool-2",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
						SpotInstOptions: map[string]string{
							"spot": "true",
						},
					},
				},
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool-2",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
							"kops.k8s.io/instance-group-role": "Node",
						},
					},
					SpotInstOptions: map[string]string{
						"spot": "true",
					},
				},
			},
			errorExpected: multierror.Append(errors.New("error no vng found with instance group name: test-kops-machine-pool-2")),
		},
		{
			description: "should fail when failing to attach",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, csg, sg, defaultSecret,
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
						},
					},
				},
			},
			errorExpected: multierror.Append(errors.New("failed to retrieve role from KopsMachinePool test-kops-machine-pool")),
		},
		{
			description: "should fail when failing to attach with karpenterKMP with Provisioner",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, csg, sg, defaultSecret,
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KarpenterProvisioners: []karpenterv1alpha5.Provisioner{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-provisioner",
							},
						},
					},
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						Manager: "Karpenter",
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
						},
					},
				},
			},
			errorExpected: multierror.Append(errors.New("failed to retrieve role from KopsMachinePool test-kops-machine-pool")),
		},
		{
			description: "should fail when failing to attach with karpenterKMP with NodePool",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, csg, sg, defaultSecret,
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxxx"),
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			kmp: &kinfrastructurev1alpha1.KopsMachinePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-kops-machine-pool",
					Labels: map[string]string{
						"cluster.x-k8s.io/cluster-name": "test-cluster",
					},
				},
				Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
					ClusterName: "test-cluster",
					KarpenterNodePools: []karpenterv1beta1.NodePool{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-node-pool",
							},
						},
					},
					KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
						Manager: "Karpenter",
						NodeLabels: map[string]string{
							"kops.k8s.io/instance-group-name": "test-ig",
						},
					},
				},
			},
			errorExpected: multierror.Append(errors.New("failed to add security group sg-1 to instance i-xxxx: random error to attach")),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, input *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				if tc.errorExpected != nil {
					return nil, errors.New("random error to attach")
				} else {
					return &awsec2.ModifyInstanceAttributeOutput{}, nil
				}
			}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				if tc.instances != nil {
					return &awsec2.DescribeInstancesOutput{
						Reservations: []ec2types.Reservation{
							{
								Instances: tc.instances,
							},
						},
					}, nil
				} else {
					return &awsec2.DescribeInstancesOutput{}, nil
				}
			}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return &awsec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []ec2types.LaunchTemplate{
						{
							LaunchTemplateId:   aws.String("lt-xxxx"),
							LaunchTemplateName: aws.String("test-launch-template"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("tag:KubernetesCluster"),
									Value: aws.String("test-cluster"),
								},
								{
									Key:   aws.String("tag:Name"),
									Value: aws.String("test-launch-template"),
								},
							},
						},
					},
				}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}

			fakeOceanClient.MockListClusters = func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test-cluster"),
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
							Labels: []*oceanaws.Label{
								{
									Key:   aws.String("kops.k8s.io/instance-group-name"),
									Value: aws.String("test-kops-machine-pool"),
								},
							},
						},
					},
				}, nil
			}
			if tc.mockUpdateLaunchSpec != nil {
				fakeOceanClient.MockUpdateLaunchSpec = tc.mockUpdateLaunchSpec
			} else {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					return &oceanaws.UpdateLaunchSpecOutput{}, nil
				}
			}

			recorder := record.NewFakeRecorder(5)

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
				Recorder: recorder,
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      sg,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.attachKopsMachinePool(ctx, csg, kcp, *tc.kmp)
			if tc.errorExpected == nil {
				if !errors.Is(err, ErrSecurityGroupNotAvailable) {
					g.Expect(err).To(BeNil())
				}

			} else {
				g.Expect(tc.errorExpected).To(MatchError(err))
			}
		})
	}
}

func TestAttachSGToVNG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		kmp                    *kinfrastructurev1alpha1.KopsMachinePool
		csg                    *crossec2v1beta1.SecurityGroup
		launchSpecs            []*oceanaws.LaunchSpec
		instances              []ec2types.Instance
		expectedSecurityGroups []string
		errorExpected          error
	}{
		{
			description: "should attach SG to VNG",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
				},
				{
					ID:   aws.String("2"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String("test-kops-machine-pool-2"),
						},
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
		{
			description: "should do nothing when SG is already attached",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
		{
			description:   "should fail if cant match vng with kmp name",
			kmp:           spotKMP,
			csg:           csg,
			launchSpecs:   []*oceanaws.LaunchSpec{},
			errorExpected: fmt.Errorf("error no vng found with instance group name: %s", spotKMP.Name),
		},
		{
			description: "should attach sg in the current vng instances",
			kmp:         spotKMP,
			csg:         csg,
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("1"),
				},
			},
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
				},
				{
					ID:   aws.String("2"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String("test-kops-machine-pool-2"),
						},
					},
				},
			},
			expectedSecurityGroups: []string{"sg-1"},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: []ec2types.Instance{
								{
									InstanceId: aws.String("i-xxx"),
									State: &ec2types.InstanceState{
										Name: ec2types.InstanceStateNameRunning,
									},
								},
								{
									InstanceId: aws.String("i-yyy"),
									State: &ec2types.InstanceState{
										Name: ec2types.InstanceStateNameTerminated,
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
				g.Expect(updateLaunchSpecInput.LaunchSpec.SecurityGroupIDs).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &oceanaws.UpdateLaunchSpecOutput{}, nil
			}
			reconciler := SecurityGroupReconciler{
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				ec2Client:               fakeEC2Client,
				log:                     ctrl.LoggerFrom(ctx),
			}

			err = reconciliation.attachSGToVNG(ctx, fakeOceanClient, tc.launchSpecs, tc.kmp.Name, csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err).To(MatchError(tc.errorExpected))
			}
		})
	}
}

func TestAttachSGToASG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		asgs                   []autoscalingtypes.AutoScalingGroup
		instances              []ec2types.Instance
		expectedSecurityGroups []string
		errorExpected          error
	}{
		{
			description: "should attach SecurityGroup to the ASG",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},

			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should attach SecurityGroup to the ASG instance",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
					Instances: []autoscalingtypes.Instance{
						{
							InstanceId: aws.String("i-xxx"),
						},
					},
				},
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should fail when not finding asg",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: errors.New("failed to retrieve AutoScalingGroup"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{
					Reservations: []ec2types.Reservation{
						{
							Instances: tc.instances,
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						LaunchTemplateId: params.LaunchTemplateId,
						VersionNumber:    aws.Int64(2),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: tc.asgs,
				}, nil
			}
			fakeASGClient.MockUpdateAutoScalingGroup = func(ctx context.Context, params *awsautoscaling.UpdateAutoScalingGroupInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.UpdateAutoScalingGroupOutput, error) {
				Expect(*params.LaunchTemplate.Version).To(BeEquivalentTo("2"))
				return &awsautoscaling.UpdateAutoScalingGroupOutput{}, nil
			}
			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				asgClient:               fakeASGClient,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.attachSGToASG(ctx, "test-asg", "sg-yyyy")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring((tc.errorExpected.Error())))
			}
		})
	}
}

func TestAttachSGToLaunchTemplate(t *testing.T) {
	testCases := []struct {
		description                       string
		k8sObjects                        []client.Object
		instances                         []ec2types.Instance
		expectedSecurityGroups            []string
		errorExpected                     error
		errorGetLaunchTemplate            bool
		errorGetLastLaunchTemplateVersion bool
		errorCreateLaunchTemplateVersion  bool
		errorGetReservations              bool
		errorAttachIGToInstances          bool
	}{
		{
			description: "should attach SecurityGroup to the launch template",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should attach SecurityGroup to the launch template instance",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
		},
		{
			description: "should fail when not finding launch template",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:          errors.New("some error when retrieving launch template"),
			errorGetLaunchTemplate: true,
		},
		{
			description: "should fail when not finding launch template version",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			expectedSecurityGroups:            []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:                     errors.New("some error when retrieving launch template version"),
			errorGetLastLaunchTemplateVersion: true,
		},
		{
			description: "should fail when not create launch template version",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			expectedSecurityGroups:           []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:                    errors.New("some error when creating launch template version"),
			errorCreateLaunchTemplateVersion: true,
		},
		{
			description: "should fail when not able to get reservations",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			expectedSecurityGroups: []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:          errors.New("some error when get reservations"),
			errorGetReservations:   true,
		},
		{
			description: "should fail when not able to attach ig to instances",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg,
			},
			instances: []ec2types.Instance{
				{
					InstanceId: aws.String("i-xxx"),
					SecurityGroups: []ec2types.GroupIdentifier{
						{
							GroupId: aws.String("sg-xxxx"),
						},
					},
					State: &ec2types.InstanceState{
						Name: ec2types.InstanceStateNameRunning,
					},
				},
			},
			expectedSecurityGroups:   []string{"sg-xxxx", "sg-yyyy"},
			errorExpected:            errors.New("some error when attach ig to instances"),
			errorAttachIGToInstances: true,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			if tc.errorGetLastLaunchTemplateVersion {
				fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
					return nil, errors.New("some error when retrieving launch template version")
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
					return &awsec2.DescribeLaunchTemplateVersionsOutput{
						LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
							{
								LaunchTemplateId: params.LaunchTemplateId,
								LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
									NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
										{
											Groups: []string{
												"sg-xxxx",
											},
										},
									},
								},
							},
						},
					}, nil
				}
			}

			if tc.errorAttachIGToInstances {
				fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
					g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return nil, errors.New("some error when attach ig to instances")
				}
			} else {
				fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
					g.Expect(params.Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return &awsec2.ModifyInstanceAttributeOutput{}, nil
				}
			}

			if tc.errorGetReservations {
				fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
					return nil, errors.New("some error when get reservations")
				}
			} else {
				fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
					return &awsec2.DescribeInstancesOutput{
						Reservations: []ec2types.Reservation{
							{
								Instances: tc.instances,
							},
						},
					}, nil
				}
			}

			if tc.errorCreateLaunchTemplateVersion {
				fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
					g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return nil, errors.New("some error when creating launch template version")
				}
			} else {
				fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
					g.Expect(params.LaunchTemplateData.NetworkInterfaces[0].Groups).To(BeEquivalentTo(tc.expectedSecurityGroups))
					return &awsec2.CreateLaunchTemplateVersionOutput{
						LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
							LaunchTemplateId: params.LaunchTemplateId,
							VersionNumber:    aws.Int64(2),
						},
					}, nil
				}
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			if tc.errorGetLaunchTemplate {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return nil, errors.New("some error when retrieving launch template")
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return &awsec2.DescribeLaunchTemplatesOutput{
						LaunchTemplates: []ec2types.LaunchTemplate{
							{
								LaunchTemplateId:   aws.String("lt-xxxx"),
								LaunchTemplateName: aws.String("test-launch-template"),
								Tags: []ec2types.Tag{
									{
										Key:   aws.String("tag:KubernetesCluster"),
										Value: aws.String("test-cluster"),
									},
									{
										Key:   aws.String("tag:Name"),
										Value: aws.String("test-launch-template"),
									},
								},
							},
						},
					}, nil
				}
			}

			reconciler := SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(3),
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.attachSGToLaunchTemplate(ctx, "test-cluster", "test-launch-template", "sg-yyyy")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring((tc.errorExpected.Error())))
			}
		})
	}
}

func TestReconcileDelete(t *testing.T) {
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		sgTarget      *securitygroupv1alpha2.SecurityGroup
		expectedError error
		FinalizersAt  []*ReferencedPool
	}{
		{
			description: "should remove crossplane security group referencing kmp and remove finalizer",
			k8sObjects: []client.Object{
				sg, csg, kcp, cluster, defaultSecret,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sgTarget: sg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should remove security group even if kmp reference doesn't exist",
			k8sObjects: []client.Object{
				sg, csg, cluster, defaultSecret,
			},
			sgTarget: sg,
		},
		{
			description: "should remove security group even if kcp reference doesn't exist",
			k8sObjects: []client.Object{
				sgKCP, csg, cluster, defaultSecret,
			},
			sgTarget: sgKCP,
		},
		{
			description: "should remove the crossplane security group referencing kmp using kops",
			k8sObjects: []client.Object{
				sg, csg, kcp, spotKMP, cluster, defaultSecret,
			},
			sgTarget: sg,
		},
		{
			description: "should remove the crossplane security group referencing kcp and remove finalizer",
			k8sObjects: []client.Object{
				sgKCP, csg, cluster, defaultSecret,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kcpWithFinalizer("test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			FinalizersAt: []*ReferencedPool{
				{
					Name:          "test-cluster",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
				{
					Name:          "test-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
			sgTarget: sgKCP,
		},
		{
			description: "should fail with infrastructureRef not supported",
			k8sObjects: []client.Object{
				sg, csg, kcp, kmp, cluster, defaultSecret,
			},
			sgTarget: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "UnsupportedKind",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			expectedError: errors.New("infrastructureRef not supported"),
		},
	}

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			fakeOceanClient.MockListClusters = func(ctx context.Context, listClusterInput *oceanaws.ListClustersInput) (*oceanaws.ListClustersOutput, error) {
				return &oceanaws.ListClustersOutput{
					Clusters: []*oceanaws.Cluster{
						{
							ID:                  aws.String("o-1"),
							ControllerClusterID: aws.String("test-cluster"),
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
							Labels: []*oceanaws.Label{
								{
									Key:   aws.String("kops.k8s.io/instance-group-name"),
									Value: aws.String("test-kops-machine-pool"),
								},
							},
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-yyyy",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
				sg:                      tc.sgTarget,
			}

			_, err = reconciliation.reconcileDelete(ctx, tc.sgTarget)
			if tc.expectedError == nil {
				g.Expect(err).ToNot(HaveOccurred())
				deletedSG := &crossec2v1beta1.SecurityGroup{}
				key := client.ObjectKey{
					Name: sg.Name,
				}
				err = fakeClient.Get(ctx, key, deletedSG)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

				deletedCSG := &crossec2v1beta1.SecurityGroup{}
				key = client.ObjectKey{
					Name: sg.Name,
				}
				err = fakeClient.Get(ctx, key, deletedCSG)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				for _, expectedFinalizerAt := range tc.FinalizersAt {
					switch expectedFinalizerAt.Kind {
					case "KopsMachinePool":
						kmp := kinfrastructurev1alpha1.KopsMachinePool{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kmp)
						if expectedFinalizerAt.Expected {
							g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
						} else {
							g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					case "KopsControlPlane":
						kcp := kcontrolplanev1alpha1.KopsControlPlane{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kcp)
						if expectedFinalizerAt.Expected {
							g.Expect(kcp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))

							kmps, _ := kops.GetKopsMachinePoolsWithLabel(ctx, fakeClient, "cluster.x-k8s.io/cluster-name", kcp.Name)
							for _, kmp := range kmps {
								g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
							}
						} else {
							g.Expect(kcp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					default:
						g.Fail("Unexpected kind")
					}
				}
			} else {
				g.Expect(err).To(MatchError(tc.expectedError))
			}
		})
	}
}

func TestDetachSGFromVNG(t *testing.T) {
	testCases := []struct {
		description            string
		kmp                    *kinfrastructurev1alpha1.KopsMachinePool
		csg                    *crossec2v1beta1.SecurityGroup
		launchSpecs            []*oceanaws.LaunchSpec
		expectedSecurityGroups []string
		errorExpected          error
		updateMockError        bool
	}{
		{
			description: "should detach SG-1 from VNG",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
						"sg-2",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-2"},
		},
		{
			description: "should do nothing if sg is not attached without error",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-2",
					},
				},
			},
			expectedSecurityGroups: []string{"sg-2"},
		},
		{
			description:   "should fail if cant match vng with kmp name",
			kmp:           spotKMP,
			csg:           csg,
			launchSpecs:   []*oceanaws.LaunchSpec{},
			errorExpected: fmt.Errorf("error no vng found with instance group name:"),
		},
		{
			description: "should fail if update return an error",
			kmp:         spotKMP,
			csg:         csg,
			launchSpecs: []*oceanaws.LaunchSpec{
				{
					ID:   aws.String("1"),
					Name: aws.String("test-vng"),
					Labels: []*oceanaws.Label{
						{
							Key:   aws.String("kops.k8s.io/instance-group-name"),
							Value: aws.String(spotKMP.Name),
						},
					},
					SecurityGroupIDs: []string{
						"sg-1",
						"sg-2",
					},
				},
			},
			updateMockError: true,
			errorExpected:   fmt.Errorf("error updating ocean cluster launch spec:"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}
			fakeOceanClient := &fakeocean.MockOceanCloudProviderAWS{}
			if tc.updateMockError {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					return nil, fmt.Errorf("error")
				}
			} else {
				fakeOceanClient.MockUpdateLaunchSpec = func(ctx context.Context, updateLaunchSpecInput *oceanaws.UpdateLaunchSpecInput) (*oceanaws.UpdateLaunchSpecOutput, error) {
					g.Expect(updateLaunchSpecInput.LaunchSpec.SecurityGroupIDs).To(Equal(tc.expectedSecurityGroups))
					return &oceanaws.UpdateLaunchSpecOutput{}, nil
				}
			}
			reconciler := SecurityGroupReconciler{
				NewOceanCloudProviderAWSFactory: func() spot.OceanClient {
					return fakeOceanClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.detachSGFromVNG(ctx, fakeOceanClient, tc.launchSpecs, tc.kmp.Name, csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}
}

func TestDetachSGFromKopsMachinePool(t *testing.T) {
	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		csg           *crossec2v1beta1.SecurityGroup
		kcp           *kcontrolplanev1alpha1.KopsControlPlane
		kmps          []kinfrastructurev1alpha1.KopsMachinePool
		errorExpected error
	}{
		{
			description: "should detach sg from kmp",
			k8sObjects: []client.Object{
				defaultSecret,
			},
			csg: csg,
			kcp: kcp,
			kmps: []kinfrastructurev1alpha1.KopsMachinePool{
				*kmp,
			},
		},
		{
			description: "should detach sg if kmp manager is karpenter with Provisioners",
			k8sObjects: []client.Object{
				defaultSecret,
			},
			csg: csg,
			kcp: kcp,
			kmps: []kinfrastructurev1alpha1.KopsMachinePool{
				*karpenterKMPProvisioner,
			},
		},
		{
			description: "should detach sg if kmp manager is karpenter with NodePool",
			k8sObjects: []client.Object{
				defaultSecret,
			},
			csg: csg,
			kcp: kcp,
			kmps: []kinfrastructurev1alpha1.KopsMachinePool{
				*karpenterKMPNodePool,
			},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
				return &awsec2.DescribeInstancesOutput{}, nil
			}
			fakeEC2Client.MockModifyInstanceAttribute = func(ctx context.Context, params *awsec2.ModifyInstanceAttributeInput, opts []func(*awsec2.Options)) (*awsec2.ModifyInstanceAttributeOutput, error) {
				return &awsec2.ModifyInstanceAttributeOutput{}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return &awsec2.DescribeLaunchTemplatesOutput{
					LaunchTemplates: []ec2types.LaunchTemplate{
						{
							LaunchTemplateId:   aws.String("lt-xxxx"),
							LaunchTemplateName: aws.String("test-launch-template"),
							Tags: []ec2types.Tag{
								{
									Key:   aws.String("tag:KubernetesCluster"),
									Value: aws.String("test-cluster"),
								},
								{
									Key:   aws.String("tag:Name"),
									Value: aws.String("test-launch-template"),
								},
							},
						},
					},
				}, nil
			}
			reconciler := SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(3),
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			ctx := context.TODO()

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			err = reconciliation.detachSGFromKopsMachinePool(ctx, tc.csg, tc.kmps...)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}

}

func TestDetachSGFromASG(t *testing.T) {
	testCases := []struct {
		description            string
		k8sObjects             []client.Object
		launchTemplateVersions []ec2types.LaunchTemplateVersion
		asgs                   []autoscalingtypes.AutoScalingGroup
		errorExpected          error
		expectedSecurityGroups []string
	}{
		{
			description: "should detach SecurityGroup from the ASG",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			launchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateId: aws.String("lt-xxxx"),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-1",
								},
							},
						},
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
		},
		{
			description: "should do nothing when SecurityGroup is already detached",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			launchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateId: aws.String("lt-xxxx"),
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-1",
								},
							},
						},
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
		},
		{
			description: "should return err when failing to retrieve ASG",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg-2"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: fmt.Errorf("ASG Not Found"),
		},
		{
			description: "should return err when failing to retrieve launch template version",
			asgs: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg,
			},
			errorExpected: fmt.Errorf("Launch Template Not Found"),
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				launchTemplateVersions := []ec2types.LaunchTemplateVersion{}
				for _, lt := range tc.launchTemplateVersions {
					if *lt.LaunchTemplateId == *params.LaunchTemplateId {
						launchTemplateVersions = append(launchTemplateVersions, lt)
					}
				}
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: launchTemplateVersions,
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				g.Expect(params.LaunchTemplateData.SecurityGroupIds).To(Equal(tc.expectedSecurityGroups))
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}
			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				asgs := []autoscalingtypes.AutoScalingGroup{}
				for _, asg := range tc.asgs {
					if *asg.AutoScalingGroupName == params.AutoScalingGroupNames[0] {
						asgs = append(asgs, asg)
					}
				}
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: asgs,
				}, nil
			}
			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				asgClient:               fakeASGClient,
				ec2Client:               fakeEC2Client,
			}

			err = reconciliation.detachSGFromASG(ctx, "test-asg", "sg-1")
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))

			}
		})
	}
}

func TestEnsureAttachReferences(t *testing.T) {
	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		errorExpected error
		csg           *crossec2v1beta1.SecurityGroup
		sg            *securitygroupv1alpha2.SecurityGroup
		FinalizersAt  []*ReferencedPool
	}{
		{
			description: "should attach sg to kmp",
			k8sObjects: []client.Object{
				defaultSecret,
				kmp,
				kcp,
				csg,
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-another-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
						Finalizers: []string{
							getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
					},
				},
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-another-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-another-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-another-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
		{
			description: "should attach sg to kcp and to related kmps and attach sg to one single kmp but not to his kcp",
			k8sObjects: []client.Object{
				defaultSecret, kmp, kcp, csg,
				kmpWithFinalizer("test-another-kops-machine-pool", "test-cluster-2", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kcpWithFinalizer("test-cluster-2", []string{}),
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha",
							Kind:       "KopsMachinePool",
							Name:       "test-another-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-another-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-another-kops-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster-2",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(sg).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      tc.sg,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			_, err = reconciliation.ensureAttachReferences(ctx, tc.csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
				for _, expectedFinalizerAt := range tc.FinalizersAt {
					switch expectedFinalizerAt.Kind {
					case "KopsMachinePool":
						kmp := kinfrastructurev1alpha1.KopsMachinePool{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kmp)
						if expectedFinalizerAt.Expected {
							g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
						} else {
							g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					case "KopsControlPlane":
						kcp := kcontrolplanev1alpha1.KopsControlPlane{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kcp)
						if expectedFinalizerAt.Expected {
							g.Expect(kcp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))

							kmps, _ := kops.GetKopsMachinePoolsWithLabel(ctx, fakeClient, "cluster.x-k8s.io/cluster-name", kcp.Name)
							for _, kmp := range kmps {
								g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
							}
						} else {
							g.Expect(kcp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					default:
						g.Fail("Unexpected kind")
					}
				}
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}
}

func TestEnsureDetachRemovedReferences(t *testing.T) {
	testCases := []struct {
		description   string
		k8sObjects    []client.Object
		errorExpected error
		csg           *crossec2v1beta1.SecurityGroup
		sg            *securitygroupv1alpha2.SecurityGroup
		FinalizersAt  []*ReferencedPool
	}{
		{
			description: "should detach sg from kmp",
			k8sObjects: []client.Object{
				defaultSecret,
				csg,
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-another-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
						Finalizers: []string{
							getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
					},
				},
				&kinfrastructurev1alpha1.KopsMachinePool{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: metav1.NamespaceDefault,
						Name:      "test-kops-machine-pool",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test-cluster",
						},
						Finalizers: []string{
							getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
						},
					},
					Spec: kinfrastructurev1alpha1.KopsMachinePoolSpec{
						ClusterName: "test-cluster",
						KopsInstanceGroupSpec: kopsapi.InstanceGroupSpec{
							NodeLabels: map[string]string{
								"kops.k8s.io/instance-group-name": "test-ig",
								"kops.k8s.io/instance-group-role": "Node",
							},
						},
					},
				},
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-another-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-another-machine-pool",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should detach sg from kcp",
			k8sObjects: []client.Object{
				defaultSecret, kcp, csg,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kcpWithFinalizer("test-cluster-2", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster-2",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should detach sg from both kcp and kmp ",
			k8sObjects: []client.Object{
				defaultSecret, kcp, csg,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kcpWithFinalizer("test-cluster-2", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
				{
					Name:          "test-cluster-2",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      false,
				},
			},
		},
		{
			description: "should not detach anything",
			k8sObjects: []client.Object{
				defaultSecret, kcp, csg,
				kmpWithFinalizer("test-kops-machine-pool", "test-cluster", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
				kcpWithFinalizer("test-cluster-2", []string{getFinalizerName(csg.Status.AtProvider.SecurityGroupID)}),
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
				Status: securitygroupv1alpha2.SecurityGroupStatus{
					Ready: true,
					AppliedInfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsControlPlane",
							Name:       "test-cluster-2",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			csg: csg,
			FinalizersAt: []*ReferencedPool{
				{
					Name:          kmp.ObjectMeta.Name,
					Namespace:     kmp.ObjectMeta.Namespace,
					Kind:          "KopsMachinePool",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
				{
					Name:          "test-cluster-2",
					Namespace:     metav1.NamespaceDefault,
					Kind:          "KopsControlPlane",
					FinalizerName: getFinalizerName(csg.Status.AtProvider.SecurityGroupID),
					Expected:      true,
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(sg).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return &awsautoscaling.DescribeAutoScalingGroupsOutput{
					AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
						{
							AutoScalingGroupName: aws.String("test-asg"),
							LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
								LaunchTemplateId: aws.String("lt-xxxx"),
								Version:          aws.String("1"),
							},
						},
					},
				}, nil
			}

			reconciler := SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      tc.sg,
				ec2Client:               reconciler.NewEC2ClientFactory(aws.Config{}),
				asgClient:               reconciler.NewAutoScalingClientFactory(aws.Config{}),
			}

			_, err = reconciliation.ensureDetachRemovedReferences(ctx, tc.csg)
			if tc.errorExpected == nil {
				g.Expect(err).To(BeNil())
				for _, expectedFinalizerAt := range tc.FinalizersAt {
					switch expectedFinalizerAt.Kind {
					case "KopsMachinePool":
						kmp := kinfrastructurev1alpha1.KopsMachinePool{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kmp)
						if expectedFinalizerAt.Expected {
							g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
						} else {
							g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}
					case "KopsControlPlane":
						kcp := kcontrolplanev1alpha1.KopsControlPlane{}
						key := client.ObjectKey{
							Name:      expectedFinalizerAt.Name,
							Namespace: expectedFinalizerAt.Namespace,
						}
						_ = fakeClient.Get(ctx, key, &kcp)
						if expectedFinalizerAt.Expected {
							g.Expect(kcp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
						} else {
							g.Expect(kcp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
						}

						kmps, _ := kops.GetKopsMachinePoolsWithLabel(ctx, fakeClient, "cluster.x-k8s.io/cluster-name", kcp.Name)
						for _, kmp := range kmps {
							if expectedFinalizerAt.Expected {
								g.Expect(kmp.Finalizers).To(ContainElement(expectedFinalizerAt.FinalizerName))
							} else {
								g.Expect(kmp.Finalizers).NotTo(ContainElement(expectedFinalizerAt.FinalizerName))
							}
						}
					default:
						g.Fail("Unexpected kind")
					}
				}
			} else {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			}
		})
	}
}

func TestSecurityGroupStatus(t *testing.T) {
	testCases := []struct {
		description                   string
		k8sObjects                    []client.Object
		mockDescribeAutoScalingGroups func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error)
		mockDescribeLaunchTemplates   func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error)
		conditionsToAssert            []*clusterv1beta1.Condition
		errorExpected                 error
		expectedReadiness             bool
		expectedInfraRef              []*corev1.ObjectReference
	}{
		{
			description: "should successfully patch SecurityGroup",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg, defaultSecret,
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition),
			},
			expectedInfraRef: []*corev1.ObjectReference{
				{
					APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsMachinePool",
					Name:       "test-kops-machine-pool",
					Namespace:  metav1.NamespaceDefault,
				},
			},
			expectedReadiness: true,
		},
		{
			description: "should mark SG ready condition as false when not available yet",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, defaultSecret, &crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Status: crossec2v1beta1.SecurityGroupStatus{
						ResourceStatus: crossplanev1.ResourceStatus{
							ConditionedStatus: crossplanev1.ConditionedStatus{
								Conditions: []crossplanev1.Condition{
									{
										Type:    "Ready",
										Status:  corev1.ConditionFalse,
										Reason:  "Unavailable",
										Message: "error message",
									},
								},
							},
						},
					},
				},
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupReadyCondition,
					"Unavailable",
					clusterv1beta1.ConditionSeverityError,
					"error message",
				),
			},
			errorExpected: ErrSecurityGroupNotAvailable,
		},
		{
			description: "should mark attach condition as false when failed to attach",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, sg, csg, defaultSecret,
			},
			mockDescribeAutoScalingGroups: func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
				return nil, errors.New("some error when attaching asg")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition,
					securitygroupv1alpha2.SecurityGroupAttachmentFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error when attaching asg"),
			},
			errorExpected: errors.New("failed to retrieve AutoScalingGroup"),
		},
		{
			description: "should mark attach condition as false when failed attach to launch template",
			k8sObjects: []client.Object{
				karpenterKMPProvisioner, cluster, kcp, sg, csg, defaultSecret,
			},
			mockDescribeLaunchTemplates: func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
				return nil, errors.New("some error when retrieving launch template")
			},
			conditionsToAssert: []*clusterv1beta1.Condition{
				conditions.TrueCondition(securitygroupv1alpha2.SecurityGroupReadyCondition),
				conditions.TrueCondition(securitygroupv1alpha2.CrossplaneResourceReadyCondition),
				conditions.FalseCondition(securitygroupv1alpha2.SecurityGroupAttachedCondition,
					securitygroupv1alpha2.SecurityGroupAttachmentFailedReason,
					clusterv1beta1.ConditionSeverityError,
					"some error when retrieving launch template"),
			},
			errorExpected: errors.New("some error when retrieving launch template"),
		},
		{
			description: "should create a SecurityGroup with infrastructureRef in the status",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, sgKCP, csg,
			},
			expectedInfraRef: []*corev1.ObjectReference{
				{
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsControlPlane",
					Name:       "test-cluster",
					Namespace:  metav1.NamespaceDefault,
				},
			},
			expectedReadiness: true,
		},
		{
			description: "should update a SecurityGroup Status with changes in infrastructureRef",
			k8sObjects: []client.Object{
				kmp, cluster, kcp, defaultSecret, csg,
				&securitygroupv1alpha2.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-security-group",
					},
					Spec: securitygroupv1alpha2.SecurityGroupSpec{
						IngressRules: []securitygroupv1alpha2.IngressRule{
							{
								IPProtocol: "TCP",
								FromPort:   40000,
								ToPort:     60000,
								AllowedCIDRBlocks: []string{
									"0.0.0.0/0",
								},
							},
						},
						InfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsControlPlane",
								Name:       "test-cluster",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
					Status: securitygroupv1alpha2.SecurityGroupStatus{
						Ready: true,
						AppliedInfrastructureRef: []*corev1.ObjectReference{
							{
								APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsMachinePool",
								Name:       "test-kops-machine-pool",
								Namespace:  metav1.NamespaceDefault,
							},
							{
								APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
								Kind:       "KopsControlPlane",
								Name:       "test-cluster",
								Namespace:  metav1.NamespaceDefault,
							},
						},
					},
				},
			},
			expectedInfraRef: []*corev1.ObjectReference{
				{
					APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
					Kind:       "KopsControlPlane",
					Name:       "test-cluster",
					Namespace:  metav1.NamespaceDefault,
				},
			},
			expectedReadiness: true,
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).WithStatusSubresource(sg).Build()
			fakeEC2Client := &fakeec2.MockEC2Client{}
			fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
				return &awsec2.DescribeVpcsOutput{
					Vpcs: []ec2types.Vpc{
						{
							VpcId: aws.String("x.x.x.x"),
						},
					},
				}, nil
			}
			fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
				return &awsec2.DescribeLaunchTemplateVersionsOutput{
					LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
						{
							LaunchTemplateId: params.LaunchTemplateId,
							LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
								NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
									{
										Groups: []string{
											"sg-xxxx",
										},
									},
								},
							},
						},
					},
				}, nil
			}
			fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
				return &awsec2.CreateLaunchTemplateVersionOutput{
					LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
						VersionNumber: aws.Int64(1),
					},
				}, nil
			}
			fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
				return &awsec2.DescribeSecurityGroupsOutput{}, nil
			}

			if tc.mockDescribeLaunchTemplates == nil {
				fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
					return &awsec2.DescribeLaunchTemplatesOutput{
						LaunchTemplates: []ec2types.LaunchTemplate{
							{
								LaunchTemplateId:   aws.String("lt-xxxx"),
								LaunchTemplateName: aws.String("test-launch-template"),
								Tags: []ec2types.Tag{
									{
										Key:   aws.String("tag:KubernetesCluster"),
										Value: aws.String("test-cluster"),
									},
									{
										Key:   aws.String("tag:Name"),
										Value: aws.String("test-launch-template"),
									},
								},
							},
						},
					}, nil
				}
			} else {
				fakeEC2Client.MockDescribeLaunchTemplates = tc.mockDescribeLaunchTemplates
			}

			fakeASGClient := &fakeasg.MockAutoScalingClient{}
			if tc.mockDescribeAutoScalingGroups == nil {
				fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
					return &awsautoscaling.DescribeAutoScalingGroupsOutput{
						AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
							{
								AutoScalingGroupName: aws.String("test-asg"),
								LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
									LaunchTemplateId: aws.String("lt-xxxx"),
									Version:          aws.String("1"),
								},
							},
						},
					}, nil
				}
			} else {
				fakeASGClient.MockDescribeAutoScalingGroups = tc.mockDescribeAutoScalingGroups
			}

			reconciler := &SecurityGroupReconciler{
				Client:   fakeClient,
				Recorder: record.NewFakeRecorder(5),
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
			}

			_, err = reconciler.Reconcile(ctx, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Name: "test-security-group",
				},
			})

			if tc.errorExpected != nil {
				g.Expect(err.Error()).To(ContainSubstring(tc.errorExpected.Error()))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			sg = &securitygroupv1alpha2.SecurityGroup{}
			key := client.ObjectKey{
				Name: "test-security-group",
			}
			err = fakeClient.Get(ctx, key, sg)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sg.Status.Conditions).ToNot(BeNil())
			g.Expect(sg.Status.Ready).To(Equal(tc.expectedReadiness))
			g.Expect(sg.Status.AppliedInfrastructureRef).To(Equal(tc.expectedInfraRef))

			if tc.conditionsToAssert != nil {
				assertConditions(g, sg, tc.conditionsToAssert...)
			}

		})
	}
}

func TestCustomMetrics(t *testing.T) {
	testCases := []struct {
		description                string
		expectedResult             float64
		reconciliationStatusResult []string
	}{
		{
			description:                "should be zero on successful reconciliation",
			expectedResult:             0.0,
			reconciliationStatusResult: []string{"succeed"},
		},
		{
			description:                "should get incremented on reconcile errors",
			expectedResult:             1.0,
			reconciliationStatusResult: []string{"fail"},
		},
		{
			description:                "should get incremented on consecutive reconcile errors",
			expectedResult:             2.0,
			reconciliationStatusResult: []string{"fail", "fail"},
		},
		{
			description:                "should be zero after a successful reconciliation",
			expectedResult:             0.0,
			reconciliationStatusResult: []string{"fail", "succeed"},
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	custommetrics.ReconciliationConsecutiveErrorsTotal.Reset()
	vfs.Context.ResetMemfsContext(true)
	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	fakeASGClient := &fakeasg.MockAutoScalingClient{}
	fakeASGClient.MockDescribeAutoScalingGroups = func(ctx context.Context, params *awsautoscaling.DescribeAutoScalingGroupsInput, optFns []func(*awsautoscaling.Options)) (*awsautoscaling.DescribeAutoScalingGroupsOutput, error) {
		return &awsautoscaling.DescribeAutoScalingGroupsOutput{
			AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{
				{
					AutoScalingGroupName: aws.String("test-asg"),
					LaunchTemplate: &autoscalingtypes.LaunchTemplateSpecification{
						LaunchTemplateId: aws.String("lt-xxxx"),
						Version:          aws.String("1"),
					},
				},
			},
		}, nil
	}
	fakeEC2Client := &fakeec2.MockEC2Client{}
	fakeEC2Client.MockDescribeSecurityGroups = func(ctx context.Context, params *awsec2.DescribeSecurityGroupsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeSecurityGroupsOutput, error) {
		return &awsec2.DescribeSecurityGroupsOutput{}, nil
	}
	fakeEC2Client.MockDescribeVpcs = func(ctx context.Context, input *awsec2.DescribeVpcsInput, opts []func(*awsec2.Options)) (*awsec2.DescribeVpcsOutput, error) {
		return &awsec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{
					VpcId: aws.String("x.x.x.x"),
				},
			},
		}, nil
	}
	fakeEC2Client.MockDescribeLaunchTemplateVersions = func(ctx context.Context, params *awsec2.DescribeLaunchTemplateVersionsInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplateVersionsOutput, error) {
		return &awsec2.DescribeLaunchTemplateVersionsOutput{
			LaunchTemplateVersions: []ec2types.LaunchTemplateVersion{
				{
					LaunchTemplateId: params.LaunchTemplateId,
					LaunchTemplateData: &ec2types.ResponseLaunchTemplateData{
						NetworkInterfaces: []ec2types.LaunchTemplateInstanceNetworkInterfaceSpecification{
							{
								Groups: []string{
									"sg-xxxx",
								},
							},
						},
					},
				},
			},
		}, nil
	}
	fakeEC2Client.MockCreateLaunchTemplateVersion = func(ctx context.Context, params *awsec2.CreateLaunchTemplateVersionInput, optFns []func(*awsec2.Options)) (*awsec2.CreateLaunchTemplateVersionOutput, error) {
		return &awsec2.CreateLaunchTemplateVersionOutput{
			LaunchTemplateVersion: &ec2types.LaunchTemplateVersion{
				VersionNumber: aws.Int64(1),
			},
		}, nil
	}
	fakeEC2Client.MockDescribeLaunchTemplates = func(ctx context.Context, params *awsec2.DescribeLaunchTemplatesInput, optFns []func(*awsec2.Options)) (*awsec2.DescribeLaunchTemplatesOutput, error) {
		return &awsec2.DescribeLaunchTemplatesOutput{
			LaunchTemplates: []ec2types.LaunchTemplate{
				{
					LaunchTemplateId:   aws.String("lt-xxxx"),
					LaunchTemplateName: aws.String("test-launch-template"),
					Tags: []ec2types.Tag{
						{
							Key:   aws.String("tag:KubernetesCluster"),
							Value: aws.String("test-cluster"),
						},
						{
							Key:   aws.String("tag:Name"),
							Value: aws.String("test-launch-template"),
						},
					},
				},
			},
		}, nil
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			custommetrics.ReconciliationConsecutiveErrorsTotal.Reset()

			fakeClient := fake.NewClientBuilder().WithObjects(kcp, cluster, defaultSecret, karpenterKMPProvisioner, sg, csg).WithScheme(scheme.Scheme).WithStatusSubresource(sg).Build()

			reconciler := &SecurityGroupReconciler{
				Client: fakeClient,
				NewEC2ClientFactory: func(cfg aws.Config) ec2.EC2Client {
					return fakeEC2Client
				},
				NewAutoScalingClientFactory: func(cfg aws.Config) autoscaling.AutoScalingClient {
					return fakeASGClient
				},
			}

			for _, reconciliationStatus := range tc.reconciliationStatusResult {
				reconciler.Recorder = record.NewFakeRecorder(10)
				// This is just to force a reconciliation error so we can increment the metric
				if reconciliationStatus == "fail" {
					fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
						return nil, errors.New("error")
					}
				} else {
					fakeEC2Client.MockDescribeInstances = func(ctx context.Context, input *awsec2.DescribeInstancesInput, opts []func(*awsec2.Options)) (*awsec2.DescribeInstancesOutput, error) {
						return &awsec2.DescribeInstancesOutput{}, nil
					}
				}
				_, _ = reconciler.Reconcile(context.TODO(), ctrl.Request{
					NamespacedName: client.ObjectKey{
						Name: sg.Name,
					},
				})

			}

			var reconciliationConsecutiveErrorsTotal dto.Metric
			g.Expect(func() error {
				g.Expect(custommetrics.ReconciliationConsecutiveErrorsTotal.WithLabelValues("securitygroup", sg.Name, "testing").Write(&reconciliationConsecutiveErrorsTotal)).To(Succeed())
				metricValue := reconciliationConsecutiveErrorsTotal.GetGauge().GetValue()
				if metricValue != tc.expectedResult {
					return fmt.Errorf("metric value differs from expected: %f != %f", metricValue, tc.expectedResult)
				}
				return nil
			}()).Should(Succeed())

		})
	}
}

func TestGetEnvironment(t *testing.T) {
	testCases := []struct {
		description         string
		k8sObjects          []client.Object
		sg                  *securitygroupv1alpha2.SecurityGroup
		expectedEnvironment string
	}{
		{
			description: "should find environment by KMP",
			k8sObjects: []client.Object{
				kmp,
				cluster,
			},
			sg:                  sg,
			expectedEnvironment: "testing",
		},
		{
			description: "should find environment by KCP",
			k8sObjects: []client.Object{
				kcp,
				cluster,
			},
			sg:                  sgKCP,
			expectedEnvironment: "testing",
		},
		{
			description: "should not find environment because cluster not exists",
			k8sObjects: []client.Object{
				kmp,
			},
			sg:                  sgKCP,
			expectedEnvironment: "environmentNotFound",
		},
		{
			description:         "should not find environment because kmp not exists",
			sg:                  sg,
			expectedEnvironment: "environmentNotFound",
		},
		{
			description: "should find environment by second KMP",
			k8sObjects: []client.Object{
				kmp,
				cluster,
			},
			sg: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-security-group",
				},
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: []securitygroupv1alpha2.IngressRule{
						{
							IPProtocol: "TCP",
							FromPort:   40000,
							ToPort:     60000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
					InfrastructureRef: []*corev1.ObjectReference{
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool-not-exists",
							Namespace:  metav1.NamespaceDefault,
						},
						{
							APIVersion: "infrastructure.cluster.x-k8s.io/v1alpha1",
							Kind:       "KopsMachinePool",
							Name:       "test-kops-machine-pool",
							Namespace:  metav1.NamespaceDefault,
						},
					},
				},
			},
			expectedEnvironment: "testing",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kinfrastructurev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = kcontrolplanev1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			reconciler := SecurityGroupReconciler{
				Client:                      fakeClient,
				NewEC2ClientFactory:         nil,
				NewAutoScalingClientFactory: nil,
			}

			reconciliation := &SecurityGroupReconciliation{
				SecurityGroupReconciler: reconciler,
				log:                     ctrl.LoggerFrom(ctx),
				sg:                      tc.sg,
				ec2Client:               nil,
				asgClient:               nil,
			}

			environment := reconciliation.getEnvironment(ctx)
			g.Expect(environment).To(Equal(tc.expectedEnvironment))
		})
	}
}

func assertConditions(g *WithT, from conditions.Getter, conditions ...*clusterv1beta1.Condition) {
	for _, condition := range conditions {
		assertCondition(g, from, condition)
	}
}

func assertCondition(g *WithT, from conditions.Getter, condition *clusterv1beta1.Condition) {
	g.Expect(conditions.Has(from, condition.Type)).To(BeTrue())

	if condition.Status == corev1.ConditionTrue {
		conditions.IsTrue(from, condition.Type)
	} else {
		conditionToBeAsserted := conditions.Get(from, condition.Type)
		g.Expect(conditionToBeAsserted.Status).To(Equal(condition.Status))
		g.Expect(conditionToBeAsserted.Severity).To(Equal(condition.Severity))
		g.Expect(conditionToBeAsserted.Reason).To(Equal(condition.Reason))
		if condition.Message != "" {
			g.Expect(conditionToBeAsserted.Message).To(ContainSubstring(condition.Message))
		}
	}
}
