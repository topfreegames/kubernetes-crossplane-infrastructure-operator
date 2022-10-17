/*
Copyright 2021.

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
	"time"

	wildlifecrossec2v1alphav1 "github.com/topfreegames/crossplane-provider-aws/apis/ec2/manualv1alpha1"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	sgv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	clmesh "github.com/topfreegames/provider-crossplane/pkg/clustermesh"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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
	"sigs.k8s.io/controller-runtime/pkg/source"
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
	log                            logr.Logger
	NewEC2ClientFactory            func(cfg aws.Config) ec2.EC2Client
	PopulateClusterSpecFactory     func(r *ClusterMeshReconciler, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1beta1.ClusterSpec, error)
	ReconcilePeeringsFactory       func(r *ClusterMeshReconciler, ctx context.Context, cluster *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) error
	ReconcileSecurityGroupsFactory func(r *ClusterMeshReconciler, ctx context.Context, cluster *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) error
	ReconcileRoutesFactory         func(r *ClusterMeshReconciler, ctx context.Context, cluster *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error)
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=vpcpeeringconnections,verbs=list;watch;create;delete
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/finalizers,verbs=update

func (r *ClusterMeshReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {
	start := time.Now()
	r.log = ctrl.LoggerFrom(ctx)

	cluster := &clusterv1beta1.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return resultError, err
	}

	clustermesh := &clustermeshv1beta1.ClusterMesh{}

	defer func() {
		if clustermesh != nil && len(clustermesh.Spec.Clusters) > 0 {
			err := r.Status().Update(ctx, clustermesh)
			if err != nil {
				r.log.Error(err, "error updating clustermesh %s status", clustermesh)
			}
			err = r.Update(ctx, clustermesh)
			if err != nil {
				r.log.Error(err, "error updating clustermesh %s", clustermesh)
			}
		}
	}()

	shouldReconcileCluster := isClusterMeshEnabled(*cluster)
	if !shouldReconcileCluster {

		// TODO: Improve how to determine that a cluster was marked for removal from a cluster group
		clusterBelongsToMesh, clustermeshName, err := r.isClusterBelongToAnyMesh(cluster.Name)
		if err != nil {
			return resultError, fmt.Errorf("error to determine if the cluster belongs to a mesh: %w", err)
		}
		if clusterBelongsToMesh {
			r.log.Info(fmt.Sprintf("starting reconcile clustermesh deletion for %s", cluster.ObjectMeta.Name))
			clustermesh.Name = clustermeshName
			err = r.reconcileDelete(ctx, cluster, clustermesh)
			if err != nil {
				return resultError, err
			}
			durationMsg := fmt.Sprintf("finished reconcile clustermesh loop for %s finished in %s ", cluster.ObjectMeta.Name, time.Since(start).String())
			r.log.Info(durationMsg)
			return resultDefault, nil
		} else {
			return resultDefault, nil
		}
	}

	result, err := r.reconcileNormal(ctx, cluster, clustermesh)
	durationMsg := fmt.Sprintf("finished reconcile clustermesh loop for %s finished in %s ", cluster.ObjectMeta.Name, time.Since(start).String())
	if result.RequeueAfter > 0 {
		durationMsg = fmt.Sprintf("%s, next run in %s", durationMsg, result.RequeueAfter.String())
	}
	r.log.Info(durationMsg)
	return result, err
}

func (r *ClusterMeshReconciler) reconcileNormal(ctx context.Context, cluster *clusterv1beta1.Cluster, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error) {
	r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s\n", cluster.ObjectMeta.Name))

	clSpec, err := r.PopulateClusterSpecFactory(r, ctx, cluster)
	if err != nil {
		return resultError, fmt.Errorf("error populating cluster spec: %w", err)
	}

	key := client.ObjectKey{
		Name: cluster.Labels[clmesh.Label],
	}

	if err := r.Get(ctx, key, clustermesh); err != nil {
		if !apierrors.IsNotFound(err) {
			return resultError, err
		}
		meshName := cluster.Labels[clmesh.Label]
		ccm := clmesh.New(meshName, clSpec)
		r.log.Info(fmt.Sprintf("creating clustermesh %s\n", ccm.ObjectMeta.GetName()))
		if err := r.Create(ctx, ccm); err != nil {
			return resultError, err
		}
		return resultDefault, nil
	} else if !r.isClusterBelongToMesh(cluster.Name, *clustermesh) {
		clustermesh.Spec.Clusters = append(clustermesh.Spec.Clusters, clSpec)
		r.log.Info(fmt.Sprintf("adding %s to clustermesh %s\n", cluster.ObjectMeta.Name, clustermesh.Name))
		// TODO: Verify if we need this with Patch
		if err := r.Update(ctx, clustermesh); err != nil {
			return resultError, err
		}
	}
	return r.reconcileExternalResources(ctx, clSpec, clustermesh)
}

func (r *ClusterMeshReconciler) reconcileExternalResources(ctx context.Context, clSpec *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error) {

	err := r.ReconcilePeeringsFactory(r, ctx, clSpec, clustermesh)
	if err != nil {
		return resultError, err
	}

	err = r.ReconcileSecurityGroupsFactory(r, ctx, clSpec, clustermesh)
	if err != nil {
		return resultError, err
	}

	result, err := r.ReconcileRoutesFactory(r, ctx, clSpec, clustermesh)
	if err != nil {
		return resultError, err
	}

	if len(clustermesh.Spec.Clusters) > 1 {
		err = r.validateClusterMesh(ctx, clustermesh)
		if err != nil {
			return resultError, err
		}
	}

	return result, nil
}

func (r *ClusterMeshReconciler) validateClusterMesh(ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error {
	vpcReady, routesReady, sgReady := true, true, true

	vpcPeeringConnections, err := crossplane.GetOwnedVPCPeeringConnections(ctx, clustermesh, r.Client)
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
	securityGroups, err := crossplane.GetOwnedSecurityGroups(ctx, clustermesh, r.Client)
	if err != nil {
		return err
	}

	for _, securityGroup := range securityGroups.Items {
		if !checkConditionsReadyAndSynced(securityGroup.Status.Conditions) {
			sgReady = false
		}
	}

	if vpcReady {
		conditions.MarkTrue(clustermesh, clustermeshv1beta1.ClusterMeshVPCPeeringReadyCondition)
	} else {
		conditions.MarkFalse(clustermesh, clustermeshv1beta1.ClusterMeshVPCPeeringReadyCondition, clustermeshv1beta1.ClusterMeshVPCPeeringFailedReason, clusterv1beta1.ConditionSeverityError, "VpcPeeringConnection not ready")
	}

	if routesReady {
		conditions.MarkTrue(clustermesh, clustermeshv1beta1.ClusterMeshRoutesReadyCondition)
	} else {
		conditions.MarkFalse(clustermesh, clustermeshv1beta1.ClusterMeshRoutesReadyCondition, clustermeshv1beta1.ClusterMeshRoutesFailedReason, clusterv1beta1.ConditionSeverityError, "Routes not ready")
	}

	if sgReady {
		conditions.MarkTrue(clustermesh, clustermeshv1beta1.ClusterMeshSecurityGroupsReadyCondition)
	} else {
		conditions.MarkFalse(clustermesh, clustermeshv1beta1.ClusterMeshSecurityGroupsReadyCondition, clustermeshv1beta1.ClusterMeshSecurityGroupFailedReason, clusterv1beta1.ConditionSeverityError, "SecurityGroups not ready")
	}

	return nil
}

func (r *ClusterMeshReconciler) reconcileDelete(ctx context.Context, cluster *clusterv1beta1.Cluster, clustermesh *clustermeshv1beta1.ClusterMesh) error {

	key := client.ObjectKey{
		Name: clustermesh.Name,
	}

	if err := r.Get(ctx, key, clustermesh); err != nil {
		return err
	}

	sg := &sgv1alpha1.SecurityGroup{}
	sgKey := client.ObjectKey{
		Name: clmesh.GetClusterMeshSecurityGroupName(cluster.Name),
	}
	if err := r.Get(ctx, sgKey, sg); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	err := r.Delete(ctx, sg)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	r.log.Info(fmt.Sprintf("deleted security group for cluster %s\n", cluster.ObjectMeta.Name))

	clustermeshCopy := clustermesh.DeepCopy()

	for _, vpcPeeringConnectionRef := range clustermeshCopy.Status.CrossplanePeeringRef {
		for _, clSpec := range clustermesh.Spec.Clusters {
			if vpcPeeringConnectionRef.Name == fmt.Sprintf("%s-%s", cluster.Name, clSpec.Name) || vpcPeeringConnectionRef.Name == fmt.Sprintf("%s-%s", clSpec.Name, cluster.Name) {
				err := crossplane.DeleteCrossplaneVPCPeeringConnection(ctx, r.Client, clustermesh, vpcPeeringConnectionRef)
				if err != nil {
					return err
				}
				break
			}
		}
	}

	for i, clSpec := range clustermesh.Spec.Clusters {
		if clSpec.Name == cluster.Name {
			clustermesh.Spec.Clusters = append(clustermesh.Spec.Clusters[:i], clustermesh.Spec.Clusters[i+1:]...)
			break
		}
	}

	if len(clustermesh.Spec.Clusters) == 0 {
		if err := r.Client.Delete(ctx, clustermesh); err != nil {
			return err
		}
	}

	return nil
}

func ReconcilePeerings(r *ClusterMeshReconciler, ctx context.Context, currentCluster *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) error {
	ownedVPCPeeringConnectionsRef, err := crossplane.GetOwnedVPCPeeringConnectionsRef(ctx, clustermesh, r.Client)
	if err != nil {
		return err
	}
	clustermesh.Status.CrossplanePeeringRef = ownedVPCPeeringConnectionsRef

	for _, cluster := range clustermesh.Spec.Clusters {
		if cmp.Equal(currentCluster, cluster) {
			continue
		}
		if !crossplane.IsVPCPeeringAlreadyCreated(clustermesh, currentCluster, cluster) {
			err := crossplane.CreateCrossplaneVPCPeeringConnection(ctx, r.Client, clustermesh, currentCluster, cluster)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
		}
	}

	return nil
}

func ReconcileRoutes(r *ClusterMeshReconciler, ctx context.Context, clSpec *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error) {
	vpcPeeringConnections, err := crossplane.GetOwnedVPCPeeringConnections(ctx, clustermesh, r.Client)
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

	clustermesh.Status.RoutesRef = routesRef

	return resultDefault, nil
}

func manageCrossplaneRoutes(r *ClusterMeshReconciler, ctx context.Context, clusterCIDR string, vpcPeeringConnection wildlifecrossec2v1alphav1.VPCPeeringConnection, clSpec *clustermeshv1beta1.ClusterSpec) error {
	vpcPeeringConnectionID := vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"]
	isRouteCreated, err := crossplane.IsRouteToVpcPeeringAlreadyCreated(ctx, clusterCIDR, vpcPeeringConnectionID, clSpec.RouteTableIDs, r.Client)
	if err != nil {
		return err
	}
	if !isRouteCreated {
		for _, routeTable := range clSpec.RouteTableIDs {
			err := crossplane.CreateCrossplaneRoute(ctx, r.Client, clSpec.Region, clusterCIDR, routeTable, vpcPeeringConnection)
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

func ReconcileSecurityGroups(r *ClusterMeshReconciler, ctx context.Context, cluster *clustermeshv1beta1.ClusterSpec, clustermesh *clustermeshv1beta1.ClusterMesh) error {
	r.log.Info(fmt.Sprintf("reconciling security group for cluster %s in clustermesh %s\n", cluster.Name, clustermesh.Name))

	ownedSecurityGroupsRefs, err := crossplane.GetOwnedSecurityGroupsRef(ctx, clustermesh, r.Client)
	if err != nil {
		return err
	}
	clustermesh.Status.CrossplaneSecurityGroupRef = ownedSecurityGroupsRefs

	var cidrResults []string
	for _, cl := range clustermesh.Spec.Clusters {
		cidrResults = append(cidrResults, cl.CIDR)
	}

	rules := []sgv1alpha1.IngressRule{
		{
			IPProtocol:        "-1", // we support icmp, udp and tcp
			FromPort:          -1,
			ToPort:            -1,
			AllowedCIDRBlocks: cidrResults,
		},
	}

	sgName := clmesh.GetClusterMeshSecurityGroupName(cluster.Name)
	r.log.Info(fmt.Sprintf("creating security group %s for cluster %s in clustermesh %s\n", sgName, cluster.Name, clustermesh.Name))
	sg := &sgv1alpha1.SecurityGroup{
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
		sg.Spec.InfrastructureRef = &infraRef
		sg.Spec.IngressRules = rules
		return nil
	})
	if err != nil {
		return err
	}
	r.log.Info(fmt.Sprintf("successfully %s security group %s for cluster %s in clustermesh %s\n", string(operationResult), sg.Name, cluster.Name, clustermesh.Name))

	err = controllerutil.SetOwnerReference(clustermesh, sg, r.Scheme)
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

func (r *ClusterMeshReconciler) isClusterBelongToAnyMesh(clusterName string) (bool, string, error) {
	clustermeshes := &clustermeshv1beta1.ClusterMeshList{}
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

func (r *ClusterMeshReconciler) isClusterBelongToMesh(clusterName string, clusterMesh clustermeshv1beta1.ClusterMesh) bool {
	for _, clSpec := range clusterMesh.Spec.Clusters {
		if clSpec.Name == clusterName {
			return true
		}
	}
	return false
}

func PopulateClusterSpec(r *ClusterMeshReconciler, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1beta1.ClusterSpec, error) {
	clusterSpec := &clustermeshv1beta1.ClusterSpec{}

	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key := client.ObjectKey{
		Namespace: cluster.Spec.ControlPlaneRef.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return clusterSpec, err
	}

	subnet, err := kops.GetSubnetFromKopsControlPlane(kcp)
	if err != nil {
		return clusterSpec, err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return clusterSpec, err
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(*region),
	)
	if err != nil {
		return clusterSpec, err
	}

	ec2Client := r.NewEC2ClientFactory(cfg)

	vpcId, err := ec2.GetVPCIdFromCIDR(ctx, ec2Client, kcp.Spec.KopsClusterSpec.NetworkCIDR)
	if err != nil {
		return clusterSpec, err
	}

	routeTableIDs, err := ec2.GetRouteTableIDsFromVPCId(ctx, ec2Client, *vpcId)
	if err != nil {
		return clusterSpec, err
	}

	clusterSpec.Name = cluster.Name
	clusterSpec.Namespace = cluster.Namespace
	clusterSpec.Region = *region
	clusterSpec.VPCID = *vpcId
	clusterSpec.CIDR = kcp.Spec.KopsClusterSpec.NetworkCIDR
	clusterSpec.RouteTableIDs = routeTableIDs

	return clusterSpec, nil
}

func isClusterMeshEnabled(cluster clusterv1beta1.Cluster) bool {
	if _, ok := cluster.Labels[clmesh.Label]; !ok {
		return false
	}

	if _, ok := cluster.Annotations[clmesh.Annotation]; !ok {
		return false
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMeshReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1beta1.Cluster{}).
		Watches(
			&source.Kind{Type: &clusterv1beta1.Cluster{}},
			handler.EnqueueRequestsFromMapFunc(r.clusterToClustersMapFunc),
		).
		Complete(r)
}

func (r *ClusterMeshReconciler) clusterToClustersMapFunc(o client.Object) []ctrl.Request {
	c, ok := o.(*clusterv1beta1.Cluster)
	if !ok {
		panic(fmt.Sprintf("Expected a Cluster but got a %T", o))
	}

	var result []ctrl.Request

	clustermesh := &clustermeshv1beta1.ClusterMesh{}

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
