package crossplane

import (
	"context"
	"fmt"

	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"

	"github.com/aws/aws-sdk-go-v2/aws"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	crossec2v1alphav1 "github.com/crossplane/provider-aws/apis/ec2/v1alpha1"
	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewCrossPlaneClusterMesh(name string, clSpec *clustermeshv1beta1.ClusterSpec) *clustermeshv1beta1.ClusterMesh {
	clusters := []*clustermeshv1beta1.ClusterSpec{clSpec}
	ccm := &clustermeshv1beta1.ClusterMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clustermeshv1beta1.ClusterMeshSpec{
			Clusters: clusters,
		},
	}
	return ccm
}

func NewCrossPlaneVPCPeeringConnection(clustermesh *clustermeshv1beta1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1beta1.ClusterSpec) *crossec2v1alphav1.VPCPeeringConnection {
	crossplaneVPCPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
			Kind:       "VPCPeeringConnection",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", peeringRequester.Name, peeringAccepter.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       clustermesh.ObjectMeta.Name,
					APIVersion: "clustermesh.infrastructure.wildlife.io/v1alpha1",
					Kind:       "ClusterMesh",
					UID:        clustermesh.ObjectMeta.UID,
				},
			},
		},
		Spec: crossec2v1alphav1.VPCPeeringConnectionSpec{
			ForProvider: crossec2v1alphav1.VPCPeeringConnectionParameters{
				Region:     peeringRequester.Region,
				PeerRegion: aws.String(peeringAccepter.Region),
				CustomVPCPeeringConnectionParameters: crossec2v1alphav1.CustomVPCPeeringConnectionParameters{
					VPCID:         aws.String(peeringRequester.VPCID),
					PeerVPCID:     aws.String(peeringAccepter.VPCID),
					AcceptRequest: true,
				},
			},
		},
	}
	return crossplaneVPCPeeringConnection
}

func NewCrossplaneSecurityGroup(sg *securitygroupv1alpha1.SecurityGroup, vpcId, region *string) *crossec2v1beta1.SecurityGroup {
	csg := &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sg.GetName(),
			Namespace: sg.GetNamespace(),
		},
		Spec: crossec2v1beta1.SecurityGroupSpec{
			ForProvider: crossec2v1beta1.SecurityGroupParameters{
				Description: fmt.Sprintf("sg %s managed by provider-crossplane", sg.GetName()),
				GroupName:   sg.GetName(),
				Ingress:     []crossec2v1beta1.IPPermission{},
				VPCID:       vpcId,
				Region:      region,
			},
		},
	}

	var ingressRules []crossec2v1beta1.IPPermission
	for _, ingressRule := range sg.Spec.IngressRules {
		ipPermission := crossec2v1beta1.IPPermission{
			FromPort:   &ingressRule.FromPort,
			ToPort:     &ingressRule.ToPort,
			IPProtocol: ingressRule.IPProtocol,
		}
		var allowedCIDRBlocks []crossec2v1beta1.IPRange
		for _, allowedCIDR := range ingressRule.AllowedCIDRBlocks {
			ipRange := crossec2v1beta1.IPRange{
				CIDRIP: allowedCIDR,
			}
			allowedCIDRBlocks = append(allowedCIDRBlocks, ipRange)
		}
		ipPermission.IPRanges = allowedCIDRBlocks
		ingressRules = append(ingressRules, ipPermission)
	}
	csg.Spec.ForProvider.Ingress = ingressRules

	return csg
}

func ManageCrossplaneSecurityGroupResource(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
	log := ctrl.LoggerFrom(ctx)
	if err := kubeClient.Create(ctx, csg); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			key := client.ObjectKey{
				Namespace: csg.ObjectMeta.Namespace,
				Name:      csg.ObjectMeta.Name,
			}
			var currentCSG crossec2v1beta1.SecurityGroup
			err := kubeClient.Get(ctx, key, &currentCSG)
			if err != nil {
				return err
			}
			currentCSG.Spec = csg.Spec

			err = kubeClient.Update(ctx, &currentCSG)
			return err
		})
		if retryErr != nil {
			return retryErr
		}
		log.Info(fmt.Sprintf("updated csg %s", csg.ObjectMeta.GetName()))
		return nil
	}
	log.Info(fmt.Sprintf("created csg %s", csg.ObjectMeta.GetName()))
	return nil
}

func CreateCrossplaneVPCPeeringConnection(ctx context.Context, kubeClient client.Client, clustermesh *clustermeshv1beta1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1beta1.ClusterSpec) error {
	log := ctrl.LoggerFrom(ctx)
	crossplaneVPCPeeringConnection := NewCrossPlaneVPCPeeringConnection(clustermesh, peeringRequester, peeringAccepter)

	err := kubeClient.Create(ctx, crossplaneVPCPeeringConnection)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	log.Info(fmt.Sprintf("created vpc peering %s", crossplaneVPCPeeringConnection.ObjectMeta.GetName()))
	vpcPeeringRef := &corev1.ObjectReference{
		APIVersion: "ec2.aws.crossplane.io/v1alpha1",
		Kind:       "VPCPeeringConnection",
		Name:       crossplaneVPCPeeringConnection.ObjectMeta.Name,
	}
	clustermesh.Status.CrossplanePeeringRef = append(clustermesh.Status.CrossplanePeeringRef, vpcPeeringRef)
	return nil
}

func DeleteCrossplaneVPCPeeringConnection(ctx context.Context, kubeClient client.Client, clustermesh *clustermeshv1beta1.ClusterMesh, vpcPeeringConnectionRef *corev1.ObjectReference) error {
	log := ctrl.LoggerFrom(ctx)
	err := kubeClient.Delete(ctx, util.ObjectReferenceToUnstructured(*vpcPeeringConnectionRef))
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	log.Info(fmt.Sprintf("deleted vpc peering %s", vpcPeeringConnectionRef.Name))

	for i, statusVPCPeeringConnectionRef := range clustermesh.Status.CrossplanePeeringRef {
		if statusVPCPeeringConnectionRef.Name == vpcPeeringConnectionRef.Name {
			clustermesh.Status.CrossplanePeeringRef = append(clustermesh.Status.CrossplanePeeringRef[:i], clustermesh.Status.CrossplanePeeringRef[i+1:]...)
			break
		}
	}

	return nil
}

func IsVPCPeeringAlreadyCreated(clustermesh *clustermeshv1beta1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1beta1.ClusterSpec) bool {
	for _, objRef := range clustermesh.Status.CrossplanePeeringRef {
		if objRef.Name == fmt.Sprintf("%s-%s", peeringRequester.Name, peeringAccepter.Name) || objRef.Name == fmt.Sprintf("%s-%s", peeringAccepter.Name, peeringRequester.Name) {
			return true
		}
	}
	return false
}

func GetSecurityGroupReadyCondition(csg *crossec2v1beta1.SecurityGroup) *crossplanev1.Condition {
	for _, condition := range csg.Status.Conditions {
		if condition.Type == "Ready" {
			return &condition
		}
	}
	return nil
}

func GetOwnedVPCPeeringConnections(ctx context.Context, owner client.Object, kubeclient client.Client) ([]*corev1.ObjectReference, error) {
	vpcPeeringConnections := &crossec2v1alphav1.VPCPeeringConnectionList{}
	err := kubeclient.List(ctx, vpcPeeringConnections)
	if err != nil {
		return nil, err
	}
	var ss []*corev1.ObjectReference

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		if util.IsOwnedByObject(&vpcPeeringConnection, owner) {
			objectRef := &corev1.ObjectReference{
				APIVersion: vpcPeeringConnection.TypeMeta.APIVersion,
				Kind:       vpcPeeringConnection.TypeMeta.Kind,
				Name:       vpcPeeringConnection.ObjectMeta.Name,
			}
			ss = append(ss, objectRef)
		}
	}
	return ss, nil
}

func GetOwnedSecurityGroups(ctx context.Context, owner client.Object, kubeclient client.Client) ([]*corev1.ObjectReference, error) {
	securityGroups := &securitygroupv1alpha1.SecurityGroupList{}
	err := kubeclient.List(ctx, securityGroups)
	if err != nil {
		return nil, err
	}
	var ss []*corev1.ObjectReference

	for _, sg := range securityGroups.Items {
		if util.IsOwnedByObject(&sg, owner) {
			objectRef := &corev1.ObjectReference{
				APIVersion: sg.TypeMeta.APIVersion,
				Kind:       sg.TypeMeta.Kind,
				Name:       sg.ObjectMeta.Name,
			}
			ss = append(ss, objectRef)
		}
	}
	return ss, nil
}
