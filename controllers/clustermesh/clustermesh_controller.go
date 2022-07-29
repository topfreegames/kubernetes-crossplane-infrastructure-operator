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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/barkimedes/go-deepcopy"
	crossec2v1alphav1 "github.com/crossplane/provider-aws/apis/ec2/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	"github.com/topfreegames/provider-crossplane/pkg/kops"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterMeshReconciler reconciles a ClusterMesh object
type ClusterMeshReconciler struct {
	client.Client
	Scheme                     *runtime.Scheme
	log                        logr.Logger
	NewEC2ClientFactory        func(cfg aws.Config) ec2.EC2Client
	PopulateClusterSpecFactory func(r *ClusterMeshReconciler, ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1beta1.ClusterSpec, error)
	ReconcilePeeringsFactory   func(r *ClusterMeshReconciler, ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error
	ReconcileRoutesFactory     func(r *ClusterMeshReconciler, ctx context.Context, cluster *clustermeshv1beta1.ClusterSpec) (ctrl.Result, error)
}

//+kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=vpcpeeringconnections,verbs=list;watch;create;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/finalizers,verbs=update

func (r *ClusterMeshReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {
	r.log = ctrl.LoggerFrom(ctx)
	cluster := &clusterv1beta1.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, err
	}

	clustermesh := &clustermeshv1beta1.ClusterMesh{}

	patchHelper, err := patch.NewHelper(clustermesh, r.Client)
	if err != nil {
		r.log.Error(err, "failed to initialize patch helper")
		return ctrl.Result{}, err
	}

	defer func() {

		if clustermesh != nil && len(clustermesh.Spec.Clusters) > 0 {
			err = patchHelper.Patch(ctx, clustermesh)
			if err != nil {
				r.log.Error(rerr, fmt.Sprintf("failed to patch clustermesh: %s", err))
				rerr = err
			}
		}
	}()

	shouldReconcileCluster := isClusterMeshEnabled(*cluster)
	if !shouldReconcileCluster {
		// TODO: Improve how to determine that a cluster was marked for removal from a cluster group
		clusterBelongsToMesh, err := r.isClusterBelongToAnyMesh(cluster.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		if clusterBelongsToMesh {
			r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))
			result, err := r.reconcileDelete(ctx, cluster, clustermesh)
			r.log.Info(fmt.Sprintf("finished reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))
			return result, err
		} else {
			return ctrl.Result{}, nil
		}
	}

	r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))

	result, err := r.reconcileNormal(ctx, cluster, clustermesh)
	r.log.Info(fmt.Sprintf("finished reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))
	return result, err
}

func (r *ClusterMeshReconciler) reconcileDelete(ctx context.Context, cluster *clusterv1beta1.Cluster, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error) {

	key := client.ObjectKey{
		Name: cluster.Labels["clusterGroup"],
	}

	if err := r.Get(ctx, key, clustermesh); err != nil {
		return ctrl.Result{}, err
	}
	for i, clSpec := range clustermesh.Spec.Clusters {
		if clSpec.Name == cluster.Name {
			clustermesh.Spec.Clusters = append(clustermesh.Spec.Clusters[:i], clustermesh.Spec.Clusters[i+1:]...)
			break
		}
	}

	if len(clustermesh.Spec.Clusters) == 0 {
		if err := r.Client.Delete(ctx, clustermesh); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else {
		if err := r.Client.Update(ctx, clustermesh); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.ReconcilePeeringsFactory(r, ctx, clustermesh); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ClusterMeshReconciler) reconcileNormal(ctx context.Context, cluster *clusterv1beta1.Cluster, clustermesh *clustermeshv1beta1.ClusterMesh) (ctrl.Result, error) {
	clSpec, err := r.PopulateClusterSpecFactory(r, ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	key := client.ObjectKey{
		Name: cluster.Labels["clusterGroup"],
	}

	if err := r.Get(ctx, key, clustermesh); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		ccm := crossplane.NewCrossPlaneClusterMesh(cluster.Labels["clusterGroup"], clSpec)
		r.log.Info(fmt.Sprintf("creating clustermesh %s", ccm.ObjectMeta.GetName()))
		if err := r.Create(ctx, ccm); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else {

		if !r.isClusterBelongToMesh(cluster.Name, *clustermesh) {
			clustermesh.Spec.Clusters = append(clustermesh.Spec.Clusters, clSpec)
			r.log.Info(fmt.Sprintf("adding %s to clustermesh %s", cluster.ObjectMeta.Name, clustermesh.Name))
			// TODO: Verify if we need this with Patch
			if err := r.Client.Update(ctx, clustermesh); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	if err := r.ReconcilePeeringsFactory(r, ctx, clustermesh); err != nil {
		return ctrl.Result{}, err
	}

	return r.ReconcileRoutesFactory(r, ctx, clSpec)
}

func ReconcilePeerings(r *ClusterMeshReconciler, ctx context.Context, clustermesh *clustermeshv1beta1.ClusterMesh) error {

	ownedVPCPeeringConnectionsRef, err := crossplane.GetOwnedVPCPeeringConnections(ctx, clustermesh, r.Client)
	if err != nil {
		return err
	}
	clustermesh.Status.CrossplanePeeringRef = ownedVPCPeeringConnectionsRef

	vpcPeeringConnectionsRefInterface, err := deepcopy.Anything(clustermesh.Status.CrossplanePeeringRef)
	if err != nil {
		return err
	}
	vpcPeeringConnectionsRefToBeDeleted := vpcPeeringConnectionsRefInterface.([]*corev1.ObjectReference)

	for _, peeringRequesterCluster := range clustermesh.Spec.Clusters {
		for _, peeringAccepterCluster := range clustermesh.Spec.Clusters {
			if cmp.Equal(peeringRequesterCluster, peeringAccepterCluster) {
				continue
			}
			if !crossplane.IsVPCPeeringAlreadyCreated(clustermesh, peeringRequesterCluster, peeringAccepterCluster) {
				err := crossplane.CreateCrossplaneVPCPeeringConnection(ctx, r.Client, clustermesh, peeringRequesterCluster, peeringAccepterCluster)
				if err != nil && !apierrors.IsAlreadyExists(err) {
					return err
				}
			} else {
				for i, actualVPCPeeringConnectionRef := range vpcPeeringConnectionsRefToBeDeleted {
					if actualVPCPeeringConnectionRef.Name == fmt.Sprintf("%s-%s", peeringRequesterCluster.Name, peeringAccepterCluster.Name) {
						vpcPeeringConnectionsRefToBeDeleted = append(vpcPeeringConnectionsRefToBeDeleted[:i], vpcPeeringConnectionsRefToBeDeleted[i+1:]...)
						break
					}
				}
			}
		}
	}

	if len(vpcPeeringConnectionsRefToBeDeleted) > 0 {
		for _, vpcPeeringConnectionsRef := range vpcPeeringConnectionsRefToBeDeleted {
			err := crossplane.DeleteCrossplaneVPCPeeringConnection(ctx, r.Client, clustermesh, vpcPeeringConnectionsRef)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func ReconcileRoutes(r *ClusterMeshReconciler, ctx context.Context, clSpec *clustermeshv1beta1.ClusterSpec) (ctrl.Result, error) {
	vpcPeeringConnections := &crossec2v1alphav1.VPCPeeringConnectionList{}
	err := r.Client.List(ctx, vpcPeeringConnections)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, vpcPeeringConnection := range vpcPeeringConnections.Items {
		ready := false
		for _, condition := range vpcPeeringConnection.Status.Conditions {
			if condition.Reason == "Available" && condition.Status == "True" {
				ready = true
			}
		}
		if !ready {
			r.log.Info("can't create routes yet, vpc " + vpcPeeringConnection.Name + " not ready, requeuing cluster " + clSpec.Name)
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
		if cmp.Equal(vpcPeeringConnection.Status.AtProvider.AccepterVPCInfo.CIDRBlock, &clSpec.CIRD) {
			err := manageCrossplaneRoutes(r, ctx, *vpcPeeringConnection.Status.AtProvider.RequesterVPCInfo.CIDRBlock, vpcPeeringConnection, clSpec)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else if cmp.Equal(vpcPeeringConnection.Status.AtProvider.RequesterVPCInfo.CIDRBlock, &clSpec.CIRD) {
			err := manageCrossplaneRoutes(r, ctx, *vpcPeeringConnection.Status.AtProvider.AccepterVPCInfo.CIDRBlock, vpcPeeringConnection, clSpec)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

func manageCrossplaneRoutes(r *ClusterMeshReconciler, ctx context.Context, clusterCIRD string, vpcPeeringConnection crossec2v1alphav1.VPCPeeringConnection, clSpec *clustermeshv1beta1.ClusterSpec) error {
	vpcPeeringConnectionID := vpcPeeringConnection.ObjectMeta.Annotations["crossplane.io/external-name"]
	isRouteCreated, err := crossplane.IsRouteToVpcPeeringAlreadyCreated(ctx, clusterCIRD, vpcPeeringConnectionID, r.Client)
	if err != nil {
		return err
	}
	if !isRouteCreated {
		for _, routeTable := range clSpec.RouteTablesIDs {
			err := crossplane.CreateCrossplaneRoute(ctx, r.Client, clSpec.Region, clusterCIRD, routeTable, vpcPeeringConnection)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ClusterMeshReconciler) isClusterBelongToAnyMesh(clusterName string) (bool, error) {
	clustermeshes := &clustermeshv1beta1.ClusterMeshList{}
	err := r.Client.List(context.TODO(), clustermeshes)
	if err != nil {
		return false, err
	}
	for _, clusterMesh := range clustermeshes.Items {
		for _, clSpec := range clusterMesh.Spec.Clusters {
			if clSpec.Name == clusterName {
				return true, nil
			}
		}
	}
	return false, nil
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

	routeTablesIDs, err := ec2.GetRouteTableIDsFromVPCId(ctx, ec2Client, *vpcId)
	if err != nil {
		return clusterSpec, err
	}

	clusterSpec.Name = cluster.Name
	clusterSpec.Region = *region
	clusterSpec.VPCID = *vpcId
	clusterSpec.CIRD = kcp.Spec.KopsClusterSpec.NetworkCIDR
	clusterSpec.RouteTablesIDs = routeTablesIDs

	return clusterSpec, nil
}

func isClusterMeshEnabled(cluster clusterv1beta1.Cluster) bool {
	if _, ok := cluster.Labels["clusterGroup"]; !ok {
		return false
	}

	if _, ok := cluster.Annotations["clustermesh.infrastructure.wildlife.io"]; !ok {
		return false
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMeshReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1beta1.Cluster{}).
		Complete(r)
}
