package crossplane

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	crossec2v1alphav1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1alpha1"
	"github.com/google/go-cmp/cmp"
	securitygroupv1alpha2 "github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	clustermeshv1alpha1 "github.com/topfreegames/provider-crossplane/api/clustermesh.infrastructure/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	testVPCId                 = "vpc-xxxxx"
	testRegion                = "us-east-1"
	defaultProviderConfigName = "default"
)

func TestGetOwnedVPCPeeringConnectionsRef(t *testing.T) {

	owner := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	testCases := []struct {
		description                        string
		vpcPeeringConnections              []client.Object
		expectedOwnedVPCPeeringConnections []*corev1.ObjectReference
	}{
		{
			description: "should return vpcpeeringconnections A-B and A-C",
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       owner.ObjectMeta.Name,
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       owner.ObjectMeta.Name,
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
			},
			expectedOwnedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "A-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should return only vpcpeeringconnections A-B",
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       owner.ObjectMeta.Name,
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "another-clustermesh",
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
			},
			expectedOwnedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alphav1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.vpcPeeringConnections...).Build()
			ownedVPCPeerings, _ := GetOwnedVPCPeeringConnectionsRef(context.TODO(), owner, fakeClient)
			g.Expect(cmp.Equal(ownedVPCPeerings, tc.expectedOwnedVPCPeeringConnections)).To(BeTrue())
		})
	}
}

func TestGetOwnedVPCPeeringConnections(t *testing.T) {

	owner := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	vpcWithOwner := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VPCPeeringConnection",
			APIVersion: vpcPeeringConnectionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "A-B",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	vpc2WithOwner := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VPCPeeringConnection",
			APIVersion: vpcPeeringConnectionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "A-C",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	testCases := []struct {
		description                        string
		objects                            []client.Object
		expectedOwnedVPCPeeringConnections []crossec2v1alphav1.VPCPeeringConnection
	}{
		{
			description: "should return vpcpeeringconnections A-B and A-C",
			objects: []client.Object{
				vpcWithOwner,
				vpc2WithOwner,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
			},
			expectedOwnedVPCPeeringConnections: []crossec2v1alphav1.VPCPeeringConnection{
				*vpcWithOwner,
				*vpc2WithOwner,
			},
		},
		{
			description: "should return only vpcpeeringconnections A-B",
			objects: []client.Object{
				vpcWithOwner,
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "another-clustermesh",
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
			},
			expectedOwnedVPCPeeringConnections: []crossec2v1alphav1.VPCPeeringConnection{
				*vpcWithOwner,
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.objects...).Build()
			ownedVPCPeerings, _ := GetOwnedVPCPeeringConnections(context.TODO(), owner, fakeClient)
			g.Expect(cmp.Equal(ownedVPCPeerings.Items, tc.expectedOwnedVPCPeeringConnections)).To(BeTrue())
		})
	}
}

func TestGetOwnedSecurityGroupsRef(t *testing.T) {
	testCases := []struct {
		description                 string
		input                       *clustermeshv1alpha1.ClusterMesh
		objects                     []client.Object
		expectedOwnedSecurityGroups []*corev1.ObjectReference
	}{
		{
			description: "should return only securitygroupRef security-group-a",
			input: &clustermeshv1alpha1.ClusterMesh{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterMesh",
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
			},
			objects: []client.Object{
				&securitygroupv1alpha2.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						Kind:       "SecurityGroup",
						APIVersion: "ec2.aws.wildlife.io/v1alpha2",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "security-group-a",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
								Kind:       "ClusterMesh",
								Name:       "test-clustermesh",
							},
						},
					},
				},
				&securitygroupv1alpha2.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						Kind:       "SecurityGroup",
						APIVersion: "ec2.aws.wildlife.io/v1alpha2",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "security-group-b",
					},
				},
			},
			expectedOwnedSecurityGroups: []*corev1.ObjectReference{
				{
					APIVersion: "ec2.aws.wildlife.io/v1alpha2",
					Kind:       "SecurityGroup",
					Name:       "security-group-a",
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := securitygroupv1alpha2.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.objects...).Build()
			ownedSecurityGroups, _ := GetOwnedSecurityGroupsRef(ctx, tc.input, fakeClient)
			g.Expect(tc.expectedOwnedSecurityGroups).To(Equal(ownedSecurityGroups))

		})
	}
}

func TestGetOwnedSecurityGroups(t *testing.T) {

	owner := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	sgWithOwner := &crossec2v1beta1.SecurityGroup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SecurityGroup",
			APIVersion: "ec2.aws.crossplane.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "sg-A",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	sg2WithOwner := &crossec2v1beta1.SecurityGroup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SecurityGroup",
			APIVersion: "ec2.aws.crossplane.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "sg-B",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	testCases := []struct {
		description                 string
		objects                     []client.Object
		expectedOwnedSecurityGroups []crossec2v1beta1.SecurityGroup
	}{
		{
			description: "should return sg A and B",
			objects: []client.Object{
				sgWithOwner,
				sg2WithOwner,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
			},
			expectedOwnedSecurityGroups: []crossec2v1beta1.SecurityGroup{
				*sgWithOwner,
				*sg2WithOwner,
			},
		},
		{
			description: "should return only vpcpeeringconnections A-B",
			objects: []client.Object{
				sgWithOwner,
				&crossec2v1beta1.SecurityGroup{
					TypeMeta: metav1.TypeMeta{
						Kind:       "SecurityGroup",
						APIVersion: "ec2.aws.crossplane.io/v1beta1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "sg-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "another-clustermesh",
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
			},
			expectedOwnedSecurityGroups: []crossec2v1beta1.SecurityGroup{
				*sgWithOwner,
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.objects...).Build()
			ownedSecurityGroups, _ := GetOwnedSecurityGroups(context.TODO(), owner, fakeClient)
			g.Expect(cmp.Equal(ownedSecurityGroups.Items, tc.expectedOwnedSecurityGroups)).To(BeTrue())
		})
	}
}

func TestGetOwnedRoutes(t *testing.T) {

	clustermesh := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	owner := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vpcPeeringConnectionAPIVersion,
			Kind:       "VPCPeeringConnection",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "vpc-a-b",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       clustermesh.ObjectMeta.Name,
					APIVersion: clustermesh.TypeMeta.APIVersion,
					Kind:       clustermesh.TypeMeta.Kind,
					UID:        clustermesh.ObjectMeta.UID,
				},
			},
		},
	}

	routeWithOwner := &crossec2v1alphav1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Route",
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "sg-A",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	route2WithOwner := &crossec2v1alphav1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Route",
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "sg-B",
			ResourceVersion: "999",
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       owner.ObjectMeta.Name,
					APIVersion: owner.TypeMeta.APIVersion,
					Kind:       owner.TypeMeta.Kind,
					UID:        owner.ObjectMeta.UID,
				},
			},
		},
	}

	testCases := []struct {
		description                 string
		objects                     []client.Object
		expectedOwnedSecurityGroups []crossec2v1alphav1.Route
	}{
		{
			description: "should return sg A and B",
			objects: []client.Object{
				routeWithOwner,
				route2WithOwner,
				clustermesh,
				owner,
				&clusterv1beta1.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-cluster",
					},
				},
			},
			expectedOwnedSecurityGroups: []crossec2v1alphav1.Route{
				*routeWithOwner,
				*route2WithOwner,
			},
		},
		{
			description: "should return only vpcpeeringconnections A-B",
			objects: []client.Object{
				routeWithOwner,
				clustermesh,
				owner,
				&crossec2v1alphav1.Route{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Route",
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "sg-C",
						OwnerReferences: []metav1.OwnerReference{
							{
								Name:       "another-clustermesh",
								APIVersion: owner.TypeMeta.APIVersion,
								Kind:       owner.TypeMeta.Kind,
								UID:        owner.ObjectMeta.UID,
							},
						},
					},
				},
			},
			expectedOwnedSecurityGroups: []crossec2v1alphav1.Route{
				*routeWithOwner,
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = clusterv1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.objects...).Build()
			ownedSecurityGroups, _ := GetOwnedRoutes(context.TODO(), owner, fakeClient)
			g.Expect(cmp.Equal(ownedSecurityGroups.Items, tc.expectedOwnedSecurityGroups)).To(BeTrue())
		})
	}
}

func TestIsVPCPeeringAlreadyCreated(t *testing.T) {

	clustermesh := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	testCases := []struct {
		description                     string
		clustermeshCrossplanePeeringRef []*corev1.ObjectReference
		peeringRequester                *clustermeshv1alpha1.ClusterSpec
		peeringAccepter                 *clustermeshv1alpha1.ClusterSpec
		expectedResult                  bool
	}{
		{
			description: "should return true for A->B with nothing created",
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedResult: false,
		},
		{
			description: "should return false for A->B with AB created",
			clustermeshCrossplanePeeringRef: []*corev1.ObjectReference{
				{
					Name: "A-B",
				},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedResult: true,
		},
		{
			description: "should return true for A->B with BA created",
			clustermeshCrossplanePeeringRef: []*corev1.ObjectReference{
				{
					Name: "B-A",
				},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedResult: true,
		},
		{
			description: "should return false for A->B with AC created",
			clustermeshCrossplanePeeringRef: []*corev1.ObjectReference{
				{
					Name: "A-C",
				},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedResult: false,
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			clustermesh.Status.CrossplanePeeringRef = tc.clustermeshCrossplanePeeringRef
			created := IsVPCPeeringAlreadyCreated(clustermesh, tc.peeringRequester, tc.peeringAccepter)
			g.Expect(created).To(Equal(tc.expectedResult))
		})
	}
}

func TestCreateCrossplaneVPCPeeringConnection(t *testing.T) {
	clustermesh := &clustermeshv1alpha1.ClusterMesh{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
			Kind:       "ClusterMesh",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "clustermesh-test",
			UID:  "xxx",
		},
	}

	testCases := []struct {
		description                   string
		vpcPeeringConnections         []client.Object
		clustermeshStatus             clustermeshv1alpha1.ClusterMeshStatus
		peeringRequester              *clustermeshv1alpha1.ClusterSpec
		peeringAccepter               *clustermeshv1alpha1.ClusterSpec
		expectedVPCPeeringConnection  *crossec2v1alphav1.VPCPeeringConnection
		expectedVPCPeeringConnections []*corev1.ObjectReference
		providerConfigName            string
	}{
		{
			description: "should create vpcPeeringConnection",
			clustermeshStatus: clustermeshv1alpha1.ClusterMeshStatus{
				CrossplanePeeringRef: []*corev1.ObjectReference{},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedVPCPeeringConnection: &crossec2v1alphav1.VPCPeeringConnection{
				TypeMeta: metav1.TypeMeta{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "A-B",
					OwnerReferences: []metav1.OwnerReference{
						{
							Name:       clustermesh.ObjectMeta.Name,
							APIVersion: clustermesh.TypeMeta.APIVersion,
							Kind:       clustermesh.TypeMeta.Kind,
							UID:        clustermesh.ObjectMeta.UID,
						},
					},
					ResourceVersion: "1",
				},
				Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
					ForProvider: crossec2v1alphav1.VPCPeeringConnectionParameters{
						Region:     "us-east-1",
						PeerRegion: aws.String("eu-central-1"),
						CustomVPCPeeringConnectionParameters: crossec2v1alphav1.CustomVPCPeeringConnectionParameters{
							VPCID:         aws.String("xxx"),
							PeerVPCID:     aws.String("yyy"),
							AcceptRequest: true,
						},
					},
				},
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should do nothing when trying to create a vpcPeeringConnection already created",
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
					},
					Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
						ResourceSpec: crossplanev1.ResourceSpec{
							ProviderConfigReference: &crossplanev1.Reference{
								Name: "default",
							},
						},
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
					},
					Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
						ResourceSpec: crossplanev1.ResourceSpec{
							ProviderConfigReference: &crossplanev1.Reference{
								Name: "default",
							},
						},
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-C",
					},
					Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
						ResourceSpec: crossplanev1.ResourceSpec{
							ProviderConfigReference: &crossplanev1.Reference{
								Name: "default",
							},
						},
					},
				},
			},
			clustermeshStatus: clustermeshv1alpha1.ClusterMeshStatus{
				CrossplanePeeringRef: []*corev1.ObjectReference{
					{
						Name:       "A-B",
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					{
						Name:       "A-C",
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					{
						Name:       "B-C",
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
				},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedVPCPeeringConnection: &crossec2v1alphav1.VPCPeeringConnection{
				TypeMeta: metav1.TypeMeta{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "A-B",
					OwnerReferences: []metav1.OwnerReference{
						{
							Name:       clustermesh.ObjectMeta.Name,
							APIVersion: clustermesh.TypeMeta.APIVersion,
							Kind:       clustermesh.TypeMeta.Kind,
							UID:        clustermesh.ObjectMeta.UID,
						},
					},
					ResourceVersion: "1",
				},
				Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
					ForProvider: crossec2v1alphav1.VPCPeeringConnectionParameters{
						Region:     "us-east-1",
						PeerRegion: aws.String("eu-central-1"),
						CustomVPCPeeringConnectionParameters: crossec2v1alphav1.CustomVPCPeeringConnectionParameters{
							VPCID:         aws.String("xxx"),
							PeerVPCID:     aws.String("yyy"),
							AcceptRequest: true,
						},
					},
				},
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "A-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "B-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should create a new vpcpeeringconnection when some are already created",
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-C",
					},
				},
			},
			clustermeshStatus: clustermeshv1alpha1.ClusterMeshStatus{
				CrossplanePeeringRef: []*corev1.ObjectReference{
					{
						Name:       "A-B",
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
					{
						Name:       "B-C",
						APIVersion: vpcPeeringConnectionAPIVersion,
						Kind:       "VPCPeeringConnection",
					},
				},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "C",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedVPCPeeringConnection: &crossec2v1alphav1.VPCPeeringConnection{
				TypeMeta: metav1.TypeMeta{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "A-B",
					OwnerReferences: []metav1.OwnerReference{
						{
							Name:       clustermesh.ObjectMeta.Name,
							APIVersion: clustermesh.TypeMeta.APIVersion,
							Kind:       clustermesh.TypeMeta.Kind,
							UID:        clustermesh.ObjectMeta.UID,
						},
					},
					ResourceVersion: "1",
				},
				Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
					ForProvider: crossec2v1alphav1.VPCPeeringConnectionParameters{
						Region:     "us-east-1",
						PeerRegion: aws.String("eu-central-1"),
						CustomVPCPeeringConnectionParameters: crossec2v1alphav1.CustomVPCPeeringConnectionParameters{
							VPCID:         aws.String("xxx"),
							PeerVPCID:     aws.String("yyy"),
							AcceptRequest: true,
						},
					},
				},
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "A-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "B-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should create vpcPeeringConnection with ProviderCOnfigReference",
			clustermeshStatus: clustermeshv1alpha1.ClusterMeshStatus{
				CrossplanePeeringRef: []*corev1.ObjectReference{},
			},
			peeringRequester: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
			},
			peeringAccepter: &clustermeshv1alpha1.ClusterSpec{
				Name:   "B",
				Region: "eu-central-1",
				VPCID:  "yyy",
			},
			expectedVPCPeeringConnection: &crossec2v1alphav1.VPCPeeringConnection{
				TypeMeta: metav1.TypeMeta{
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "A-B",
					OwnerReferences: []metav1.OwnerReference{
						{
							Name:       clustermesh.ObjectMeta.Name,
							APIVersion: clustermesh.TypeMeta.APIVersion,
							Kind:       clustermesh.TypeMeta.Kind,
							UID:        clustermesh.ObjectMeta.UID,
						},
					},
					ResourceVersion: "1",
				},
				Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
					ForProvider: crossec2v1alphav1.VPCPeeringConnectionParameters{
						Region:     "us-east-1",
						PeerRegion: aws.String("eu-central-1"),
						CustomVPCPeeringConnectionParameters: crossec2v1alphav1.CustomVPCPeeringConnectionParameters{
							VPCID:         aws.String("xxx"),
							PeerVPCID:     aws.String("yyy"),
							AcceptRequest: true,
						},
					},
				},
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "A-B",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
			providerConfigName: "test",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = crossec2v1alphav1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			var providerConfigName string
			if tc.providerConfigName != "" {
				providerConfigName = tc.providerConfigName
			} else {
				providerConfigName = defaultProviderConfigName
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.vpcPeeringConnections...).Build()
			clustermesh.Status = tc.clustermeshStatus
			err = CreateCrossplaneVPCPeeringConnection(ctx, fakeClient, clustermesh, tc.peeringRequester, tc.peeringAccepter, providerConfigName)
			g.Expect(err).ToNot(HaveOccurred())
			key := client.ObjectKey{
				Name: fmt.Sprintf("%s-%s", tc.peeringRequester.Name, tc.peeringAccepter.Name),
			}
			vpcPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{}
			err = fakeClient.Get(ctx, key, vpcPeeringConnection)
			g.Expect(err).To(BeNil())
			g.Expect(vpcPeeringConnection).ToNot(BeNil())
			g.Expect(cmp.Equal(vpcPeeringConnection.Status, tc.expectedVPCPeeringConnection.Status)).To(BeTrue())
			g.Expect(vpcPeeringConnection.Spec.ProviderConfigReference.Name).To(Equal(providerConfigName))
		})
	}
}

func TestDeleteCrossplaneVPCPeeringConnection(t *testing.T) {
	testCases := []struct {
		description                   string
		clustermesh                   *clustermeshv1alpha1.ClusterMesh
		vpcPeeringConnections         []client.Object
		vpcPeeringToBeDeleted         *corev1.ObjectReference
		expectedVPCPeeringConnections []*corev1.ObjectReference
	}{
		{
			description: "should remove A-B VPCPeeringConnection",
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "A-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "B-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-C",
					},
				},
			},
			vpcPeeringToBeDeleted: &corev1.ObjectReference{
				Name:       "A-B",
				APIVersion: vpcPeeringConnectionAPIVersion,
				Kind:       "VPCPeeringConnection",
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "B-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "A-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should do nothing when removing a VPCPeeringConnection already deleted",
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
						{
							Name:       "B-C",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-C",
					},
					Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
						ResourceSpec: crossplanev1.ResourceSpec{
							ProviderConfigReference: &crossplanev1.Reference{
								Name: "default",
							},
						},
					},
				},
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "B-C",
					},
					Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
						ResourceSpec: crossplanev1.ResourceSpec{
							ProviderConfigReference: &crossplanev1.Reference{
								Name: "default",
							},
						},
					},
				},
			},
			vpcPeeringToBeDeleted: &corev1.ObjectReference{
				Name:       "A-B",
				APIVersion: vpcPeeringConnectionAPIVersion,
				Kind:       "VPCPeeringConnection",
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{
				{
					Name:       "B-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
				{
					Name:       "A-C",
					APIVersion: vpcPeeringConnectionAPIVersion,
					Kind:       "VPCPeeringConnection",
				},
			},
		},
		{
			description: "should remove the only VPCPeeringConnection",
			clustermesh: &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clustermesh",
				},
				Status: clustermeshv1alpha1.ClusterMeshStatus{
					CrossplanePeeringRef: []*corev1.ObjectReference{
						{
							Name:       "A-B",
							APIVersion: vpcPeeringConnectionAPIVersion,
							Kind:       "VPCPeeringConnection",
						},
					},
				},
			},
			vpcPeeringConnections: []client.Object{
				&crossec2v1alphav1.VPCPeeringConnection{
					TypeMeta: metav1.TypeMeta{
						Kind:       "VPCPeeringConnection",
						APIVersion: vpcPeeringConnectionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "A-B",
					},
				},
			},
			vpcPeeringToBeDeleted: &corev1.ObjectReference{
				Name:       "A-B",
				APIVersion: vpcPeeringConnectionAPIVersion,
				Kind:       "VPCPeeringConnection",
			},
			expectedVPCPeeringConnections: []*corev1.ObjectReference{},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1alphav1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.vpcPeeringConnections...).Build()
			err = DeleteCrossplaneVPCPeeringConnection(ctx, fakeClient, tc.clustermesh, tc.vpcPeeringToBeDeleted)
			key := client.ObjectKey{
				Name: tc.vpcPeeringToBeDeleted.Name,
			}

			vpcPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{}
			err = fakeClient.Get(ctx, key, vpcPeeringConnection)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())

			for _, expectedVPCPeeringConnectionRef := range tc.expectedVPCPeeringConnections {
				key = client.ObjectKey{
					Name: expectedVPCPeeringConnectionRef.Name,
				}
				expectedVPCPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{}
				err = fakeClient.Get(ctx, key, expectedVPCPeeringConnection)
				g.Expect(err).To(BeNil())
			}
			g.Expect(cmp.Equal(tc.expectedVPCPeeringConnections, tc.clustermesh.Status.CrossplanePeeringRef))

		})
	}
}

func TestCrossPlaneClusterMeshResource(t *testing.T) {
	testCases := []map[string]interface{}{
		{
			"description":   "should create crossplane clustermesh object",
			"expectedError": false,
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := clustermeshv1alpha1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc["description"].(string), func(t *testing.T) {
			cluster := &clusterv1beta1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: metav1.NamespaceDefault,
					Name:      "test-cluster",
					Annotations: map[string]string{
						"clustermesh.infrastructure.wildlife.io": "testmesh",
					},
					Labels: map[string]string{
						"clusterGroup": "testmesh",
						"environment":  "prod",
						"region":       "us-east-1",
					},
				},
			}
			clSpec := &clustermeshv1alpha1.ClusterSpec{
				Name:  "test-cluster",
				VPCID: "vpc-asidjasidiasj",
			}
			sg := &clustermeshv1alpha1.ClusterMesh{
				ObjectMeta: metav1.ObjectMeta{
					Name: cluster.Labels["clusterGroup"],
				},
				Spec: clustermeshv1alpha1.ClusterMeshSpec{
					Clusters: []*clustermeshv1alpha1.ClusterSpec{
						clSpec,
					},
				},
			}
			g.Expect(sg.ObjectMeta.Name).To(ContainSubstring("testmesh"))
		})
	}
}

func TestNewCrossplaneSecurityGroup(t *testing.T) {
	testCases := []struct {
		description        string
		ingressRules       []securitygroupv1alpha2.IngressRule
		providerConfigName string
	}{
		{
			description:  "should return a Crossplane SecurityGroup",
			ingressRules: []securitygroupv1alpha2.IngressRule{},
		},
		{
			description:        "should create a crossplane sg with the provided providerConfigName",
			ingressRules:       []securitygroupv1alpha2.IngressRule{},
			providerConfigName: "test",
		},
	}
	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			sg := &securitygroupv1alpha2.SecurityGroup{
				Spec: securitygroupv1alpha2.SecurityGroupSpec{
					IngressRules: tc.ingressRules,
				},
			}

			var expectedProviderConfigName string
			if tc.providerConfigName != "" {
				expectedProviderConfigName = tc.providerConfigName
			} else {
				expectedProviderConfigName = defaultProviderConfigName
			}
			csg := NewCrossplaneSecurityGroup(sg, &testVPCId, &testRegion, expectedProviderConfigName)
			g.Expect(csg.Spec.ForProvider.Description).To(Equal(fmt.Sprintf("sg %s managed by provider-crossplane", sg.GetName())))
			g.Expect(csg.Spec.ProviderConfigReference.Name).To(Equal(expectedProviderConfigName))
		})
	}
}

func TestCreateOrUpdateCrossplaneSecurityGroup(t *testing.T) {
	testCases := []struct {
		description           string
		k8sObjects            []client.Object
		wildlifeSecurityGroup *securitygroupv1alpha2.SecurityGroup
		validateOutput        func(sg *securitygroupv1alpha2.SecurityGroup, csg *crossec2v1beta1.SecurityGroup) bool
	}{
		{
			description: "should create crossplane security group object",
			wildlifeSecurityGroup: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
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
			validateOutput: func(_ *securitygroupv1alpha2.SecurityGroup, csg *crossec2v1beta1.SecurityGroup) bool {
				if len(csg.Spec.ForProvider.Ingress) != 1 {
					return false
				}
				if csg.Name != "test-sg" {
					return false
				}
				return true
			},
		},
		{
			description: "should update crossplane security group object",
			k8sObjects: []client.Object{
				&crossec2v1beta1.SecurityGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-sg",
					},
					Spec: crossec2v1beta1.SecurityGroupSpec{
						ForProvider: crossec2v1beta1.SecurityGroupParameters{
							Region:      aws.String("us-east-1"),
							Description: "test-sg",
							GroupName:   "test-sg",
							VPCID:       &testVPCId,
						},
					},
				},
			},
			wildlifeSecurityGroup: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
					Annotations: map[string]string{
						securitygroupv1alpha2.ReasonReconcilePaused: "true",
					},
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
			validateOutput: func(_ *securitygroupv1alpha2.SecurityGroup, csg *crossec2v1beta1.SecurityGroup) bool {
				if len(csg.Spec.ForProvider.Ingress) != 2 {
					return false
				}
				if csg.Name != "test-sg" {
					return false
				}
				if !reflect.DeepEqual(csg.Annotations, map[string]string{
					securitygroupv1alpha2.ReasonReconcilePaused: "true",
				}) {
					return false
				}
				return true
			},
		},
		{
			description: "should correctly deal with different port numbers",
			wildlifeSecurityGroup: &securitygroupv1alpha2.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-sg",
					Annotations: map[string]string{
						securitygroupv1alpha2.ReasonReconcilePaused: "true",
					},
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
						{
							IPProtocol: "TCP",
							FromPort:   20000,
							ToPort:     40000,
							AllowedCIDRBlocks: []string{
								"0.0.0.0/0",
							},
						},
					},
				},
			},
			validateOutput: func(sg *securitygroupv1alpha2.SecurityGroup, csg *crossec2v1beta1.SecurityGroup) bool {
				if len(csg.Spec.ForProvider.Ingress) != len(sg.Spec.IngressRules) {
					return false
				}
				if csg.Name != "test-sg" {
					return false
				}
				for i, csgRule := range csg.Spec.ForProvider.Ingress {
					sgRule := sg.Spec.IngressRules[i]
					if *csgRule.FromPort != sgRule.FromPort ||
						*csgRule.ToPort != sgRule.ToPort ||
						csgRule.IPProtocol != sgRule.IPProtocol {
						return false
					}
				}
				return reflect.DeepEqual(csg.Annotations, map[string]string{
					securitygroupv1alpha2.ReasonReconcilePaused: "true",
				})
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()

			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.k8sObjects...).Build()

			_, err := CreateOrUpdateCrossplaneSecurityGroup(ctx, fakeClient, aws.String("vpc-xxx"), aws.String("us-east-1"), defaultProviderConfigName, tc.wildlifeSecurityGroup)
			g.Expect(err).To(BeNil())
			csg := &crossec2v1beta1.SecurityGroup{}
			key := client.ObjectKey{
				Name: "test-sg",
			}
			err = fakeClient.Get(context.TODO(), key, csg)
			g.Expect(err).To(BeNil())
			g.Expect(csg).NotTo(BeNil())
			if tc.validateOutput != nil {
				g.Expect(tc.validateOutput(tc.wildlifeSecurityGroup, csg)).To(BeTrue())
			}

		})
	}
}

func TestGetSecurityGroupAvailableCondition(t *testing.T) {
	csg := &crossec2v1beta1.SecurityGroup{
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
		},
	}

	testCases := []struct {
		description       string
		conditions        []crossplanev1.Condition
		expectedCondition *crossplanev1.Condition
	}{
		{
			description: "should return ready condition",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: &crossplanev1.Condition{
				Type:   "Ready",
				Status: corev1.ConditionTrue,
			},
		},
		{
			description: "should return empty when missing ready condition",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Synced",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: nil,
		},
		{
			description: "should return ready condition with multiple conditions",
			conditions: []crossplanev1.Condition{
				{
					Type:   "Synced",
					Status: corev1.ConditionTrue,
				},
				{
					Type:   "Ready",
					Status: corev1.ConditionTrue,
				},
			},
			expectedCondition: &crossplanev1.Condition{
				Type:   "Ready",
				Status: corev1.ConditionTrue,
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1beta1.SchemeBuilder.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			csg.Status.Conditions = tc.conditions
			condition := GetSecurityGroupReadyCondition(csg)
			g.Expect(condition).To(Equal(tc.expectedCondition))
		})
	}
}

func TestIsRouteToVpcPeeringAlreadyCreated(t *testing.T) {

	route := []client.Object{
		&crossec2v1alphav1.Route{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "ec2.aws.crossplane.io/v1alpha1",
				Kind:       "Route",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "route-A-ab",
				UID:  "xxx",
			},
			Spec: crossec2v1alphav1.RouteSpec{
				ForProvider: crossec2v1alphav1.RouteParameters{
					DestinationCIDRBlock: aws.String("bbbb"),
					CustomRouteParameters: crossec2v1alphav1.CustomRouteParameters{
						VPCPeeringConnectionID: aws.String("ab"),
						RouteTableID:           aws.String("rt-xxxx"),
					},
				},
			},
		},
	}

	testCases := []struct {
		description            string
		vpcPeeringConnectionID string
		clusterSpec            *clustermeshv1alpha1.ClusterSpec
		destinationCIDRBlock   string
		route                  []client.Object
		routeTableIDs          []string
		expectedResult         bool
	}{
		{
			description: "should return true for cidr bbbb to vpcPeering ab",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
				},
			},
			destinationCIDRBlock:   "bbbb",
			vpcPeeringConnectionID: "ab",
			route:                  route,
			expectedResult:         true,
		},
		{
			description: "should return false for cidr bbbb to vpcPeering ac",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
				},
			},
			destinationCIDRBlock:   "cccc",
			vpcPeeringConnectionID: "ac",
			route:                  route,
			expectedResult:         false,
		},
		{
			description: "should return false for cidr cccc to vpcPeering ab",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
				},
			},
			destinationCIDRBlock:   "cccc",
			vpcPeeringConnectionID: "ab",
			route:                  route,
			expectedResult:         false,
		},
		{
			description: "should return false if no roule is found",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
				},
			},
			destinationCIDRBlock:   "bbbb",
			vpcPeeringConnectionID: "ab",
			route:                  []client.Object{},
			expectedResult:         false,
		},
		{
			description: "should return false if roule does not exists in both route tables",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			destinationCIDRBlock:   "bbbb",
			vpcPeeringConnectionID: "ab",
			routeTableIDs: []string{
				"rt-xxxx",
				"rt-zzzz",
			},
			route:          []client.Object{},
			expectedResult: false,
		},
		{
			description: "should return false if roule does not exists in both route tables but exists in one of them",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				RouteTableIDs: []string{
					"rt-xxxx",
					"rt-zzzz",
				},
			},
			destinationCIDRBlock:   "bbbb",
			vpcPeeringConnectionID: "ab",
			route:                  route,
			expectedResult:         false,
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.route...).Build()

			result, _ := IsRouteToVpcPeeringAlreadyCreated(ctx, tc.destinationCIDRBlock, tc.vpcPeeringConnectionID, tc.clusterSpec.RouteTableIDs, fakeClient)

			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestCreateCrossplaneRoute(t *testing.T) {

	vpcPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VPCPeeringConnection",
			APIVersion: vpcPeeringConnectionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "peering-A-B",
			UID:  "xxx",
			Annotations: map[string]string{
				"crossplane.io/external-name": "pcx-a-b",
			},
		},
	}

	testCases := []struct {
		description        string
		routeTable         string
		clusterSpec        *clustermeshv1alpha1.ClusterSpec
		expectedResult     bool
		providerConfigName string
	}{
		{
			description: "should create route",
			routeTable:  "rt-xxx",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
				CIDR:   "aaaa",
			},
			expectedResult: true,
		},
		{
			description: "should create route with providerConfigReference",
			routeTable:  "rt-xxx",
			clusterSpec: &clustermeshv1alpha1.ClusterSpec{
				Name:   "A",
				Region: "us-east-1",
				VPCID:  "xxx",
				CIDR:   "aaaa",
			},
			expectedResult:     true,
			providerConfigName: "test",
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			ctx := context.TODO()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

			var providerConfigName string
			if tc.providerConfigName != "" {
				providerConfigName = tc.providerConfigName
			} else {
				providerConfigName = defaultProviderConfigName
			}

			err := CreateCrossplaneRoute(ctx, fakeClient, tc.clusterSpec.Region, tc.clusterSpec.CIDR, providerConfigName, tc.routeTable, *vpcPeeringConnection)
			g.Expect(err).To(BeNil())
			route := crossec2v1alphav1.Route{}
			err = fakeClient.Get(ctx, client.ObjectKey{Name: tc.routeTable + "-" + vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"]}, &route)
			g.Expect(err).To(BeNil())
			g.Expect(route.Spec.ForProvider.RouteTableID).To(BeEquivalentTo(aws.String(tc.routeTable)))
			g.Expect(route.Spec.ForProvider.DestinationCIDRBlock).To(BeEquivalentTo(aws.String(tc.clusterSpec.CIDR)))
			g.Expect(route.Spec.ForProvider.VPCPeeringConnectionID).To(BeEquivalentTo(aws.String(vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"])))
			g.Expect(route.Spec.ProviderConfigReference.Name).To(Equal(providerConfigName))
		})
	}
}

func TestGetOwnedRoutesRef(t *testing.T) {

	owner := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VPCPeeringConnection",
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "a-b",
		},
	}

	testCases := []struct {
		description         string
		routes              []client.Object
		expectedOwnedRoutes []*corev1.ObjectReference
	}{
		{
			description: "should return owned route for VPC Peering a-b",
			routes: []client.Object{
				&crossec2v1alphav1.Route{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "ec2.aws.crossplane.io/v1alpha1",
						Kind:       "Route",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "rt-xxxx-pcx-xxxx",
						UID:  "xxx",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "ec2.aws.crossplane.io/v1alpha1",
								Kind:       "VPCPeeringConnection",
								Name:       "a-b",
							},
						},
					},
				},
			},
			expectedOwnedRoutes: []*corev1.ObjectReference{
				{
					APIVersion: "ec2.aws.crossplane.io/v1alpha1",
					Kind:       "Route",
					Name:       "rt-xxxx-pcx-xxxx",
				},
			},
		},
	}

	RegisterFailHandler(Fail)
	g := NewWithT(t)

	err := crossec2v1alphav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.routes...).Build()
			ownedRoutes, _ := GetOwnedRoutesRef(context.TODO(), owner, fakeClient)
			g.Expect(cmp.Equal(ownedRoutes, tc.expectedOwnedRoutes)).To(BeTrue())
		})
	}
}
