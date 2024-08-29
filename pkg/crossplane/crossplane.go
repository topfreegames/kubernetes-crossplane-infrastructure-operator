package crossplane

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clustermeshv1alpha1 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/clustermesh.infrastructure/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"

	"github.com/aws/aws-sdk-go-v2/aws"
	crossec2v1alphav1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1alpha1"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/google/go-cmp/cmp"
	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	vpcPeeringConnectionAPIVersion = "ec2.aws.crossplane.io/v1alpha1"
)

func NewCrossPlaneVPCPeeringConnection(clustermesh *clustermeshv1alpha1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1alpha1.ClusterSpec, providerConfigName string) *crossec2v1alphav1.VPCPeeringConnection {
	crossplaneVPCPeeringConnection := &crossec2v1alphav1.VPCPeeringConnection{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vpcPeeringConnectionAPIVersion,
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
			ResourceSpec: crossplanev1.ResourceSpec{
				ProviderConfigReference: &crossplanev1.Reference{
					Name: providerConfigName,
				},
			},
		},
	}
	return crossplaneVPCPeeringConnection
}

func NewCrossplaneSecurityGroup(sg *securitygroupv1alpha2.SecurityGroup, vpcId, region *string, providerConfigName string) *crossec2v1beta1.SecurityGroup {
	csg := &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        sg.GetName(),
			Annotations: sg.Annotations,
		},
		Spec: crossec2v1beta1.SecurityGroupSpec{
			ForProvider: crossec2v1beta1.SecurityGroupParameters{
				Description: fmt.Sprintf("sg %s managed by kubernetes-crossplane-infrastructure-operator", sg.GetName()),
				GroupName:   sg.GetName(),
				Ingress:     []crossec2v1beta1.IPPermission{},
				VPCID:       vpcId,
				Region:      region,
				Tags: []crossec2v1beta1.Tag{
					{
						Key:   "ManagedBy",
						Value: "kubernetes-crossplane-infrastructure-operator",
					},
				},
			},
			ResourceSpec: crossplanev1.ResourceSpec{
				ProviderConfigReference: &crossplanev1.Reference{
					Name: providerConfigName,
				},
			},
		},
	}
	return csg
}

func NewCrossplaneRoute(region, destinationCIDRBlock, routeTable string, providerConfigName string, vpcPeeringConnection crossec2v1alphav1.VPCPeeringConnection) *crossec2v1alphav1.Route {
	vpcPeeringConnectionID := vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"]
	croute := &crossec2v1alphav1.Route{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ec2.aws.crossplane.io/v1alpha1",
			Kind:       "Route",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: routeTable + "-" + vpcPeeringConnectionID,
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:       vpcPeeringConnection.ObjectMeta.Name,
					Kind:       vpcPeeringConnection.TypeMeta.Kind,
					APIVersion: vpcPeeringConnection.TypeMeta.APIVersion,
					UID:        vpcPeeringConnection.ObjectMeta.UID,
				},
			},
		},
		Spec: crossec2v1alphav1.RouteSpec{
			ForProvider: crossec2v1alphav1.RouteParameters{
				Region:               region,
				DestinationCIDRBlock: &destinationCIDRBlock,
				CustomRouteParameters: crossec2v1alphav1.CustomRouteParameters{
					RouteTableID:           &routeTable,
					VPCPeeringConnectionID: &vpcPeeringConnectionID,
				},
			},
			ResourceSpec: crossplanev1.ResourceSpec{
				ProviderConfigReference: &crossplanev1.Reference{
					Name: providerConfigName,
				},
			},
		},
	}

	return croute
}

func CreateOrUpdateCrossplaneSecurityGroup(ctx context.Context, kubeClient client.Client, vpcId, region *string, providerConfigName, clusterName string, sg *securitygroupv1alpha2.SecurityGroup) (*crossec2v1beta1.SecurityGroup, error) {
	csg := NewCrossplaneSecurityGroup(sg, vpcId, region, providerConfigName)
	_, err := controllerutil.CreateOrUpdate(ctx, kubeClient, csg, func() error {
		var ingressRules []crossec2v1beta1.IPPermission
		for _, ingressRule := range sg.Spec.IngressRules {
			// https://github.com/golang/go/discussions/56010
			ingressRule := ingressRule
			ipPermission := crossec2v1beta1.IPPermission{
				IPProtocol: ingressRule.IPProtocol,
			}

			if ingressRule.ToPort != 0 {
				ipPermission.ToPort = &ingressRule.ToPort
			}

			if ingressRule.FromPort != 0 {
				ipPermission.FromPort = &ingressRule.FromPort
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

		for _, infraRef := range sg.Spec.InfrastructureRef {
			switch infraRef.Kind {
			case "KopsMachinePool":
				if checkTagAlreadyExists(csg.Spec.ForProvider.Tags, fmt.Sprintf("kops.k8s.io/instance-group/%s", infraRef.Name)) {
					continue
				}
				csg.Spec.ForProvider.Tags = append(csg.Spec.ForProvider.Tags, crossec2v1beta1.Tag{
					Key:   fmt.Sprintf("kops.k8s.io/instance-group/%s", infraRef.Name),
					Value: "owned",
				})
			case "KopsControlPlane":
				kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, kubeClient, "cluster.x-k8s.io/cluster-name", clusterName)
				if err != nil {
					return err
				}

				for _, kmp := range kmps {
					if checkTagAlreadyExists(csg.Spec.ForProvider.Tags, fmt.Sprintf("kops.k8s.io/instance-group/%s", kmp.Name)) {
						continue
					}
					csg.Spec.ForProvider.Tags = append(csg.Spec.ForProvider.Tags, crossec2v1beta1.Tag{
						Key:   fmt.Sprintf("kops.k8s.io/instance-group/%s", kmp.Name),
						Value: "owned",
					})
				}
			default:
				continue
			}

		}

		csg.Spec.ForProvider.Ingress = ingressRules
		csg.Annotations = sg.Annotations
		csg.Spec.ResourceSpec.ProviderConfigReference = &crossplanev1.Reference{Name: providerConfigName}
		csg.Spec.ForProvider.VPCID = vpcId

		// varias propriedades não váo ser atualizadas se o csg já existir...

		return nil
	})
	if err != nil {
		return nil, err
	}

	return csg, nil
}

func CreateCrossplaneVPCPeeringConnection(ctx context.Context, kubeClient client.Client, clustermesh *clustermeshv1alpha1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1alpha1.ClusterSpec, providerConfigName string) error {
	log := ctrl.LoggerFrom(ctx)
	crossplaneVPCPeeringConnection := NewCrossPlaneVPCPeeringConnection(clustermesh, peeringRequester, peeringAccepter, providerConfigName)

	err := kubeClient.Create(ctx, crossplaneVPCPeeringConnection)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	log.Info(fmt.Sprintf("created vpc peering %s", crossplaneVPCPeeringConnection.ObjectMeta.GetName()))
	vpcPeeringRef := &corev1.ObjectReference{
		APIVersion: vpcPeeringConnectionAPIVersion,
		Kind:       "VPCPeeringConnection",
		Name:       crossplaneVPCPeeringConnection.ObjectMeta.Name,
	}
	clustermesh.Status.CrossplanePeeringRef = append(clustermesh.Status.CrossplanePeeringRef, vpcPeeringRef)
	return nil
}

func CreateCrossplaneRoute(ctx context.Context, kubeClient client.Client, region string, destinationCIDRBlock, providerConfigName string, routeTable string, vpcPeeringConnection crossec2v1alphav1.VPCPeeringConnection) error {
	log := ctrl.LoggerFrom(ctx)
	crossplaneRoute := NewCrossplaneRoute(region, destinationCIDRBlock, routeTable, providerConfigName, vpcPeeringConnection)

	err := kubeClient.Create(ctx, crossplaneRoute)
	if err != nil {
		return err
	}
	log.Info(fmt.Sprintf("created route  %s", crossplaneRoute.ObjectMeta.GetName()))
	return nil
}

func DeleteCrossplaneVPCPeeringConnection(ctx context.Context, kubeClient client.Client, clustermesh *clustermeshv1alpha1.ClusterMesh, vpcPeeringConnectionRef *corev1.ObjectReference) error {
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

func IsVPCPeeringAlreadyCreated(clustermesh *clustermeshv1alpha1.ClusterMesh, peeringRequester, peeringAccepter *clustermeshv1alpha1.ClusterSpec) bool {
	for _, objRef := range clustermesh.Status.CrossplanePeeringRef {
		if objRef.Name == fmt.Sprintf("%s-%s", peeringRequester.Name, peeringAccepter.Name) || objRef.Name == fmt.Sprintf("%s-%s", peeringAccepter.Name, peeringRequester.Name) {
			return true
		}
	}
	return false
}

func IsRouteToVpcPeeringAlreadyCreated(ctx context.Context, clusterCIDR, vpcPeeringConnectionID string, routeTableIDs []string, kubeclient client.Client) (bool, error) {
	routes := &crossec2v1alphav1.RouteList{}
	err := kubeclient.List(ctx, routes)
	if err != nil {
		return false, err
	}
	if len(routes.Items) == 0 {
		return false, nil
	}
	for _, routeTableID := range routeTableIDs {
		routeCreated := false
		for _, route := range routes.Items {
			if cmp.Equal(route.Spec.ForProvider.DestinationCIDRBlock, &clusterCIDR) && cmp.Equal(route.Spec.ForProvider.VPCPeeringConnectionID, &vpcPeeringConnectionID) && cmp.Equal(route.Spec.ForProvider.RouteTableID, &routeTableID) {
				routeCreated = true
			}
		}
		if !routeCreated {
			return false, nil
		}
	}

	return true, nil
}

func GetSecurityGroupReadyCondition(csg *crossec2v1beta1.SecurityGroup) *crossplanev1.Condition {
	for _, condition := range csg.Status.ResourceStatus.Conditions {
		if condition.Type == "Ready" {
			return &condition
		}
	}
	return nil
}

func GetOwnedVPCPeeringConnectionsRef(ctx context.Context, owner client.Object, kubeclient client.Client) ([]*corev1.ObjectReference, error) {
	vpcPeeringConnections := &crossec2v1alphav1.VPCPeeringConnectionList{}
	err := kubeclient.List(ctx, vpcPeeringConnections)
	if err != nil {
		return nil, err
	}
	var vpcPeeringsRef []*corev1.ObjectReference

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		if util.IsOwnedByObject(&vpcPeeringConnection, owner) {
			objectRef := &corev1.ObjectReference{
				APIVersion: vpcPeeringConnection.TypeMeta.APIVersion,
				Kind:       vpcPeeringConnection.TypeMeta.Kind,
				Name:       vpcPeeringConnection.ObjectMeta.Name,
			}
			vpcPeeringsRef = append(vpcPeeringsRef, objectRef)
		}
	}
	return vpcPeeringsRef, nil
}

func GetOwnedSecurityGroupsRef(ctx context.Context, owner client.Object, kubeclient client.Client) ([]*corev1.ObjectReference, error) {
	securityGroups := &securitygroupv1alpha2.SecurityGroupList{}
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

func GetOwnedVPCPeeringConnections(ctx context.Context, owner client.Object, kubeclient client.Client) (*crossec2v1alphav1.VPCPeeringConnectionList, error) {
	vpcPeeringConnections := &crossec2v1alphav1.VPCPeeringConnectionList{}
	err := kubeclient.List(ctx, vpcPeeringConnections)
	if err != nil {
		return nil, err
	}
	var ownedVPCPeeringConnections crossec2v1alphav1.VPCPeeringConnectionList

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		if util.IsOwnedByObject(&vpcPeeringConnection, owner) {
			ownedVPCPeeringConnections.Items = append(ownedVPCPeeringConnections.Items, vpcPeeringConnection)
		}
	}

	return &ownedVPCPeeringConnections, nil
}

func GetOwnedSecurityGroups(ctx context.Context, owner client.Object, kubeclient client.Client) (*crossec2v1beta1.SecurityGroupList, error) {
	securityGroups := &crossec2v1beta1.SecurityGroupList{}
	err := kubeclient.List(ctx, securityGroups)
	if err != nil {
		return nil, err
	}
	var ownedSecurityGroups crossec2v1beta1.SecurityGroupList

	for _, securityGroup := range securityGroups.Items {
		if util.IsOwnedByObject(&securityGroup, owner) {
			ownedSecurityGroups.Items = append(ownedSecurityGroups.Items, securityGroup)
		}
	}

	return &ownedSecurityGroups, nil
}

func GetOwnedRoutes(ctx context.Context, owner client.Object, kubeclient client.Client) (*crossec2v1alphav1.RouteList, error) {
	routes := &crossec2v1alphav1.RouteList{}
	err := kubeclient.List(ctx, routes)
	if err != nil {
		return nil, err
	}
	var ownedRoutes crossec2v1alphav1.RouteList

	for _, route := range routes.Items {
		if util.IsOwnedByObject(&route, owner) {
			ownedRoutes.Items = append(ownedRoutes.Items, route)
		}
	}

	return &ownedRoutes, nil
}

func GetOwnedRoutesRef(ctx context.Context, owner client.Object, kubeclient client.Client) ([]*corev1.ObjectReference, error) {
	routes := &crossec2v1alphav1.RouteList{}
	err := kubeclient.List(ctx, routes)
	if err != nil {
		return nil, err
	}

	var ss []*corev1.ObjectReference
	for _, route := range routes.Items {
		if util.IsOwnedByObject(&route, owner) {
			objectRef := &corev1.ObjectReference{
				Kind:       route.TypeMeta.Kind,
				Name:       route.ObjectMeta.Name,
				APIVersion: route.TypeMeta.APIVersion,
			}
			ss = append(ss, objectRef)
		}
	}

	return ss, nil
}

func checkTagAlreadyExists(tags []crossec2v1beta1.Tag, key string) bool {
	for _, tag := range tags {
		if tag.Key == key {
			return true
		}
	}
	return false
}
