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

package clustermesh

import (
	"context"
	"fmt"
	"strings"
	"time"

	crossec2v1alphav1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1alpha1"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"
	clustermeshv1alpha1 "github.com/topfreegames/provider-crossplane/api/clustermesh.infrastructure/v1alpha1"
	securitygroupv1alpha2 "github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	kopsutils "github.com/topfreegames/provider-crossplane/pkg/kops"

	"github.com/aws/aws-sdk-go-v2/aws"
	crossplanev1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	GroupLabel       = "clusterGroup"
	EnableAnnotation = "clustermesh.infrastructure.wildlife.io"
)

var (
	requeue1min   = ctrl.Result{RequeueAfter: 1 * time.Minute}
	resultDefault = ctrl.Result{RequeueAfter: 1 * time.Hour}
	resultError   = ctrl.Result{RequeueAfter: 30 * time.Minute}
)

// ClusterMeshReconciler reconciles a ClusterMesh object
type ClusterMeshReconciler struct {
	client.Client
	Scheme                         *runtime.Scheme
	NewEC2ClientFactory            func(cfg aws.Config) ec2.EC2Client
	PopulateClusterSpecFactory     func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1alpha1.ClusterSpec, error)
	ReconcilePeeringsFactory       func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error
	ReconcileSecurityGroupsFactory func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error
	ReconcileRoutesFactory         func(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) (ctrl.Result, error)
}

type ClusterMeshReconciliation struct {
	ClusterMeshReconciler
	log                logr.Logger
	start              time.Time
	ec2Client          ec2.EC2Client
	kcp                *kcontrolplanev1alpha1.KopsControlPlane
	clustermesh        *clustermeshv1alpha1.ClusterMesh
	cluster            *clusterv1beta1.Cluster
	providerConfigName string
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=vpcpeeringconnections,verbs=list;watch;create;delete
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list

func DefaultReconciler(mgr manager.Manager) *ClusterMeshReconciler {
	r := &ClusterMeshReconciler{
		Client:                         mgr.GetClient(),
		Scheme:                         mgr.GetScheme(),
		NewEC2ClientFactory:            ec2.NewEC2Client,
		PopulateClusterSpecFactory:     PopulateClusterSpec,
		ReconcilePeeringsFactory:       ReconcilePeerings,
		ReconcileSecurityGroupsFactory: ReconcileSecurityGroups,
		ReconcileRoutesFactory:         ReconcileRoutes,
	}

	return r
}

func New(name string, clSpecs ...*clustermeshv1alpha1.ClusterSpec) *clustermeshv1alpha1.ClusterMesh {
	ccm := &clustermeshv1alpha1.ClusterMesh{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clustermeshv1alpha1.ClusterMeshSpec{
			Clusters: clSpecs,
		},
	}
	return ccm
}

func getClusterMeshSecurityGroupName(clusterName string) string {
	return "clustermesh-" + clusterName + "-sg"
}

func (c *ClusterMeshReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r := &ClusterMeshReconciliation{
		ClusterMeshReconciler: *c,
		start:                 time.Now(),
		log:                   ctrl.LoggerFrom(ctx),
	}

	cluster := &clusterv1beta1.Cluster{}
	if err := c.Get(ctx, req.NamespacedName, cluster); err != nil {
		return resultError, err
	}

	if cluster.Spec.ControlPlaneRef == nil || cluster.Spec.ControlPlaneRef.Kind != "KopsControlPlane" {
		return resultDefault, fmt.Errorf("controlPlaneRef not specified or not supported")
	}

	r.kcp = &kcontrolplanev1alpha1.KopsControlPlane{}
	key := client.ObjectKey{
		Namespace: cluster.Spec.ControlPlaneRef.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}
	if err := c.Get(ctx, key, r.kcp); err != nil {
		return requeue1min, err
	}

	region, err := kopsutils.GetRegionFromKopsControlPlane(ctx, r.kcp)
	if err != nil {
		return requeue1min, fmt.Errorf("error retrieving region: %w", err)
	}

	providerConfigName, cfg, err := kopsutils.RetrieveAWSCredentialsFromKCP(ctx, c.Client, region, r.kcp)
	if err != nil {
		return resultError, err
	}

	r.providerConfigName = providerConfigName

	r.ec2Client = r.NewEC2ClientFactory(*cfg)

	r.clustermesh = &clustermeshv1alpha1.ClusterMesh{}
	r.cluster = cluster

	return r.Reconcile(ctx, req)
}

func (r *ClusterMeshReconciliation) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	defer func() {
		if r.clustermesh != nil && len(r.clustermesh.Spec.Clusters) > 0 {
			clustermeshHelper := r.clustermesh.DeepCopy()
			err := r.Patch(ctx, r.clustermesh, client.Merge)
			if err != nil {
				r.log.Error(err, fmt.Sprintf("error patching clustermesh %v", r.clustermesh))
			}
			r.clustermesh.Status = clustermeshHelper.Status
			err = r.Status().Update(ctx, r.clustermesh)
			if err != nil {
				r.log.Error(err, fmt.Sprintf("error updating clustermesh %v status", r.clustermesh))
			}
		}
	}()

	shouldReconcileCluster := isClusterMeshEnabled(*r.cluster)
	if !shouldReconcileCluster {

		// TODO: Improve how to determine that a cluster was marked for removal from a cluster group
		clusterBelongsToMesh, clustermeshName, err := r.isClusterBelongToAnyMesh(r.cluster.Name)
		if err != nil {
			return resultError, fmt.Errorf("error to determine if the cluster belongs to a mesh: %w", err)
		}
		if clusterBelongsToMesh {
			r.log.Info(fmt.Sprintf("starting reconcile clustermesh deletion for %s", r.cluster.ObjectMeta.Name))
			r.clustermesh.Name = clustermeshName
			err = r.reconcileDelete(ctx)
			if err != nil {
				return resultError, err
			}
			durationMsg := fmt.Sprintf("finished reconcile clustermesh loop for %s finished in %s ", r.cluster.ObjectMeta.Name, time.Since(r.start).String())
			r.log.Info(durationMsg)
			return resultDefault, nil
		} else {
			return resultDefault, nil
		}
	}

	result, err := r.reconcileNormal(ctx)
	durationMsg := fmt.Sprintf("finished reconcile clustermesh loop for %s finished in %s ", r.cluster.ObjectMeta.Name, time.Since(r.start).String())
	if result.RequeueAfter > 0 {
		durationMsg = fmt.Sprintf("%s, next run in %s", durationMsg, result.RequeueAfter.String())
	}
	r.log.Info(durationMsg)
	return result, err
}

func (r *ClusterMeshReconciliation) reconcileNormal(ctx context.Context) (ctrl.Result, error) {
	r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s\n", r.cluster.ObjectMeta.Name))

	clSpec, err := r.PopulateClusterSpecFactory(r, ctx, r.cluster)
	if err != nil {
		return resultError, fmt.Errorf("error populating cluster spec: %w", err)
	}

	key := client.ObjectKey{
		Name: r.cluster.Labels[GroupLabel],
	}

	if err := r.Get(ctx, key, r.clustermesh); err != nil {
		if !apierrors.IsNotFound(err) {
			return resultError, err
		}
		meshName := r.cluster.Labels[GroupLabel]
		ccm := New(meshName, clSpec)
		r.log.Info(fmt.Sprintf("creating clustermesh %s\n", ccm.ObjectMeta.GetName()))
		if err := r.Create(ctx, ccm); err != nil {
			return resultError, err
		}
		return resultDefault, nil
	}

	if !r.isClusterBelongToMesh(r.cluster.Name) {
		r.clustermesh.Spec.Clusters = append(r.clustermesh.Spec.Clusters, clSpec)
		r.log.Info(fmt.Sprintf("adding %s to clustermesh %s\n", r.cluster.ObjectMeta.Name, r.clustermesh.Name))
		// TODO: Verify if we need this with Patch
		if err := r.Update(ctx, r.clustermesh); err != nil {
			return resultError, err
		}
	}

	return r.reconcileExternalResources(ctx, clSpec)
}

func (r *ClusterMeshReconciliation) reconcileExternalResources(ctx context.Context, clSpec *clustermeshv1alpha1.ClusterSpec) (ctrl.Result, error) {
	err := r.ReconcilePeeringsFactory(r, ctx, clSpec)
	if err != nil {
		return resultError, err
	}

	err = r.ReconcileSecurityGroupsFactory(r, ctx, clSpec)
	if err != nil {
		return resultError, err
	}

	result, err := r.ReconcileRoutesFactory(r, ctx, clSpec)
	if err != nil {
		return resultError, err
	}

	if len(r.clustermesh.Spec.Clusters) > 1 {
		err = r.validateClusterMesh(ctx)
		if err != nil {
			return resultError, err
		}
	}

	return result, nil
}

func (r *ClusterMeshReconciliation) validateClusterMesh(ctx context.Context) error {
	vpcReady, routesReady, sgReady := true, true, true

	vpcPeeringConnections, err := crossplane.GetOwnedVPCPeeringConnections(ctx, r.clustermesh, r.Client)
	if err != nil {
		return err
	}

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		if !checkConditionsReadyAndSynced(vpcPeeringConnection.Status.Conditions) {
			vpcReady = false
		}

		routes, err := crossplane.GetOwnedRoutes(ctx, &vpcPeeringConnection, r.Client)
		if err != nil {
			return err
		}

		for _, route := range routes.Items {
			if !checkConditionsReadyAndSynced(route.Status.Conditions) {
				routesReady = false
			}
		}

	}
	securityGroups, err := crossplane.GetOwnedSecurityGroups(ctx, r.clustermesh, r.Client)
	if err != nil {
		return err
	}

	for _, securityGroup := range securityGroups.Items {
		if !checkConditionsReadyAndSynced(securityGroup.Status.Conditions) {
			sgReady = false
		}
	}

	if vpcReady {
		conditions.MarkTrue(r.clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition)
	} else {
		conditions.MarkFalse(r.clustermesh, clustermeshv1alpha1.ClusterMeshVPCPeeringReadyCondition, clustermeshv1alpha1.ClusterMeshVPCPeeringFailedReason, clusterv1beta1.ConditionSeverityError, "VpcPeeringConnection not ready")
	}

	if routesReady {
		conditions.MarkTrue(r.clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition)
	} else {
		conditions.MarkFalse(r.clustermesh, clustermeshv1alpha1.ClusterMeshRoutesReadyCondition, clustermeshv1alpha1.ClusterMeshRoutesFailedReason, clusterv1beta1.ConditionSeverityError, "Routes not ready")
	}

	if sgReady {
		conditions.MarkTrue(r.clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition)
	} else {
		conditions.MarkFalse(r.clustermesh, clustermeshv1alpha1.ClusterMeshSecurityGroupsReadyCondition, clustermeshv1alpha1.ClusterMeshSecurityGroupFailedReason, clusterv1beta1.ConditionSeverityError, "SecurityGroups not ready")
	}

	return nil
}

func (r *ClusterMeshReconciliation) reconcileDelete(ctx context.Context) error {
	key := client.ObjectKey{
		Name: r.clustermesh.Name,
	}

	if err := r.Get(ctx, key, r.clustermesh); err != nil {
		return err
	}

	sg := &securitygroupv1alpha2.SecurityGroup{}
	sgKey := client.ObjectKey{
		Name: getClusterMeshSecurityGroupName(r.cluster.Name),
	}
	for i, securityGroupRef := range r.clustermesh.Status.CrossplaneSecurityGroupRef {
		if securityGroupRef.Name == sgKey.Name {
			r.clustermesh.Status.CrossplaneSecurityGroupRef = append(r.clustermesh.Status.CrossplaneSecurityGroupRef[:i], r.clustermesh.Status.CrossplaneSecurityGroupRef[i+1:]...)
		}
	}
	err := r.Get(ctx, sgKey, sg)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		err := r.Delete(ctx, sg)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	r.log.Info(fmt.Sprintf("deleted security group for cluster %s\n", r.cluster.ObjectMeta.Name))

	clustermeshCopy := r.clustermesh.DeepCopy()

	for _, vpcPeeringConnectionRef := range clustermeshCopy.Status.CrossplanePeeringRef {
		if strings.Contains(vpcPeeringConnectionRef.Name, r.cluster.Name) {
			err := crossplane.DeleteCrossplaneVPCPeeringConnection(ctx, r.Client, r.clustermesh, vpcPeeringConnectionRef)
			if err != nil {
				return err
			}
		}
	}

	for i, clSpec := range r.clustermesh.Spec.Clusters {
		if clSpec.Name == r.cluster.Name {
			r.clustermesh.Spec.Clusters = append(r.clustermesh.Spec.Clusters[:i], r.clustermesh.Spec.Clusters[i+1:]...)
			break
		}
	}

	if len(r.clustermesh.Spec.Clusters) == 0 {
		if err := r.Client.Delete(ctx, r.clustermesh); err != nil {
			return err
		}
	}

	return nil
}

func ReconcilePeerings(r *ClusterMeshReconciliation, ctx context.Context, currentCluster *clustermeshv1alpha1.ClusterSpec) error {
	ownedVPCPeeringConnectionsRef, err := crossplane.GetOwnedVPCPeeringConnectionsRef(ctx, r.clustermesh, r.Client)
	if err != nil {
		return err
	}
	r.clustermesh.Status.CrossplanePeeringRef = ownedVPCPeeringConnectionsRef

	for _, cluster := range r.clustermesh.Spec.Clusters {
		if cmp.Equal(currentCluster, cluster) {
			continue
		}
		if !crossplane.IsVPCPeeringAlreadyCreated(r.clustermesh, currentCluster, cluster) {
			err := crossplane.CreateCrossplaneVPCPeeringConnection(ctx, r.Client, r.clustermesh, currentCluster, cluster, r.providerConfigName)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
		}
	}

	return nil
}

func ReconcileRoutes(r *ClusterMeshReconciliation, ctx context.Context, clSpec *clustermeshv1alpha1.ClusterSpec) (ctrl.Result, error) {
	vpcPeeringConnections, err := crossplane.GetOwnedVPCPeeringConnections(ctx, r.clustermesh, r.Client)
	if err != nil {
		return resultError, err
	}

	var routesRef []*corev1.ObjectReference

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		statusReady := checkConditionsReadyAndSynced(vpcPeeringConnection.Status.Conditions)

		if !statusReady {
			r.log.Info("can't create routes yet, vpc " + vpcPeeringConnection.Name + " not ready, requeuing cluster " + clSpec.Name)
			return requeue1min, nil
		}
		if cmp.Equal(vpcPeeringConnection.Status.AtProvider.AccepterVPCInfo.CIDRBlock, &clSpec.CIDR) {
			err := manageCrossplaneRoutes(r, ctx, *vpcPeeringConnection.Status.AtProvider.RequesterVPCInfo.CIDRBlock, vpcPeeringConnection, clSpec)
			if err != nil {
				return resultError, err
			}
		} else if cmp.Equal(vpcPeeringConnection.Status.AtProvider.RequesterVPCInfo.CIDRBlock, &clSpec.CIDR) {
			err := manageCrossplaneRoutes(r, ctx, *vpcPeeringConnection.Status.AtProvider.AccepterVPCInfo.CIDRBlock, vpcPeeringConnection, clSpec)
			if err != nil {
				return resultError, err
			}
		}

		ownedRoutesRef, err := crossplane.GetOwnedRoutesRef(ctx, &vpcPeeringConnection, r.Client)
		if err != nil {
			return resultError, err
		}
		routesRef = append(routesRef, ownedRoutesRef...)
	}

	r.clustermesh.Status.RoutesRef = routesRef

	return resultDefault, nil
}

func manageCrossplaneRoutes(r *ClusterMeshReconciliation, ctx context.Context, clusterCIDR string, vpcPeeringConnection crossec2v1alphav1.VPCPeeringConnection, clSpec *clustermeshv1alpha1.ClusterSpec) error {
	vpcPeeringConnectionID := vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"]
	isRouteCreated, err := crossplane.IsRouteToVpcPeeringAlreadyCreated(ctx, clusterCIDR, vpcPeeringConnectionID, clSpec.RouteTableIDs, r.Client)
	if err != nil {
		return err
	}
	if !isRouteCreated {
		for _, routeTable := range clSpec.RouteTableIDs {
			err := crossplane.CreateCrossplaneRoute(ctx, r.Client, clSpec.Region, clusterCIDR, r.providerConfigName, routeTable, vpcPeeringConnection)
			if err != nil {
				if apierrors.IsAlreadyExists(err) {
					continue
				}
				return err
			}
		}
	}
	return nil
}

func ReconcileSecurityGroups(r *ClusterMeshReconciliation, ctx context.Context, cluster *clustermeshv1alpha1.ClusterSpec) error {
	r.log.Info(fmt.Sprintf("reconciling security group for cluster %s in clustermesh %s\n", cluster.Name, r.clustermesh.Name))

	ownedSecurityGroupsRefs, err := crossplane.GetOwnedSecurityGroupsRef(ctx, r.clustermesh, r.Client)
	if err != nil {
		return err
	}
	r.clustermesh.Status.CrossplaneSecurityGroupRef = ownedSecurityGroupsRefs

	var cidrResults []string
	for _, cl := range r.clustermesh.Spec.Clusters {
		cidrResults = append(cidrResults, cl.CIDR)
	}

	rules := []securitygroupv1alpha2.IngressRule{
		{
			IPProtocol:        "-1", // we support icmp, udp and tcp
			FromPort:          -1,
			ToPort:            -1,
			AllowedCIDRBlocks: cidrResults,
		},
	}

	sgName := getClusterMeshSecurityGroupName(cluster.Name)
	r.log.Info(fmt.Sprintf("creating security group %s for cluster %s in clustermesh %s\n", sgName, cluster.Name, r.clustermesh.Name))
	sg := &securitygroupv1alpha2.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: sgName,
		},
	}

	infraRef := corev1.ObjectReference{
		Kind:       "KopsControlPlane",
		APIVersion: "controlplane.cluster.x-k8s.io/v1alpha1",
		Name:       cluster.Name,
		Namespace:  cluster.Namespace,
	}

	operationResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, sg, func() error {
		sg.Spec.InfrastructureRef = []*corev1.ObjectReference{
			&infraRef,
		}
		sg.Spec.IngressRules = rules
		return nil
	})
	if err != nil {
		return err
	}
	r.log.Info(fmt.Sprintf("successfully %s security group %s for cluster %s in clustermesh %s\n", string(operationResult), sg.Name, cluster.Name, r.clustermesh.Name))

	err = controllerutil.SetOwnerReference(r.clustermesh, sg, r.ClusterMeshReconciler.Scheme)
	if err != nil {
		return err
	}

	if err := r.Client.Update(ctx, sg); err != nil {
		return err
	}

	return nil
}

func checkConditionsReadyAndSynced(listConditions []crossplanev1.Condition) bool {
	resultReady, resultSynced := false, false
	for _, condition := range listConditions {
		if condition.Type == "Ready" && condition.Reason == "Available" && condition.Status == "True" {
			resultReady = true
		}
		if condition.Type == "Synced" && condition.Reason == "ReconcileSuccess" && condition.Status == "True" {
			resultSynced = true
		}
	}
	if resultReady && resultSynced {
		return true
	} else {
		return false
	}
}

func (r *ClusterMeshReconciliation) isClusterBelongToAnyMesh(clusterName string) (bool, string, error) {
	clustermeshes := &clustermeshv1alpha1.ClusterMeshList{}
	err := r.Client.List(context.TODO(), clustermeshes)
	if err != nil {
		return false, "", err
	}
	for _, clusterMesh := range clustermeshes.Items {
		for _, clSpec := range clusterMesh.Spec.Clusters {
			if clSpec.Name == clusterName {
				return true, clusterMesh.Name, nil
			}
		}
	}
	return false, "", nil
}

func (r *ClusterMeshReconciliation) isClusterBelongToMesh(clusterName string) bool {
	for _, clSpec := range r.clustermesh.Spec.Clusters {
		if clSpec.Name == clusterName {
			return true
		}
	}
	return false
}

func PopulateClusterSpec(r *ClusterMeshReconciliation, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1alpha1.ClusterSpec, error) {
	clusterSpec := &clustermeshv1alpha1.ClusterSpec{}

	subnet, err := kops.GetSubnetFromKopsControlPlane(r.kcp)
	if err != nil {
		return clusterSpec, err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return clusterSpec, err
	}

	vpcId, err := ec2.GetVPCIdWithCIDRAndClusterName(ctx, r.ec2Client, r.kcp.Name, r.kcp.Spec.KopsClusterSpec.Networking.NetworkCIDR)
	if err != nil {
		return clusterSpec, err
	}

	routeTableIDs, err := ec2.GetRouteTableIDsFromVPCId(ctx, r.ec2Client, *vpcId)
	if err != nil {
		return clusterSpec, err
	}

	clusterSpec.Name = cluster.Name
	clusterSpec.Namespace = cluster.Namespace
	clusterSpec.Region = *region
	clusterSpec.VPCID = *vpcId
	clusterSpec.CIDR = r.kcp.Spec.KopsClusterSpec.Networking.NetworkCIDR
	clusterSpec.RouteTableIDs = routeTableIDs

	return clusterSpec, nil
}

func isClusterMeshEnabled(cluster clusterv1beta1.Cluster) bool {
	if _, ok := cluster.Labels[GroupLabel]; !ok {
		return false
	}

	if _, ok := cluster.Annotations[EnableAnnotation]; !ok {
		return false
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMeshReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1beta1.Cluster{}).
		Watches(
			&clusterv1beta1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.clusterToClustersMapFunc),
		).
		Complete(r)
}

func (r *ClusterMeshReconciler) clusterToClustersMapFunc(ctx context.Context, o client.Object) []reconcile.Request {
	c, ok := o.(*clusterv1beta1.Cluster)
	if !ok {
		panic(fmt.Sprintf("Expected a Cluster but got a %T", o))
	}

	var result []ctrl.Request

	clustermesh := &clustermeshv1alpha1.ClusterMesh{}

	key := client.ObjectKey{
		Name: c.Labels["clusterGroup"],
	}

	err := r.Get(context.TODO(), key, clustermesh)
	if err != nil {
		if apierrors.IsNotFound(err) {
			name := client.ObjectKey{Namespace: c.Namespace, Name: c.Name}
			result = append(result, ctrl.Request{NamespacedName: name})
			return result
		}
	}

	for _, clSpec := range clustermesh.Spec.Clusters {
		name := client.ObjectKey{Namespace: clSpec.Namespace, Name: clSpec.Name}
		result = append(result, ctrl.Request{NamespacedName: name})
	}
	return result

}
