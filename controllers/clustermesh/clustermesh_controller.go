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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	"github.com/topfreegames/provider-crossplane/pkg/kops"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterMeshReconciler reconciles a ClusterMesh object
type ClusterMeshReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	log                 logr.Logger
	NewEC2ClientFactory func(cfg aws.Config) ec2.EC2Client
}

//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=clustermesh.infrastructure.wildlife.io,resources=clustermeshes/finalizers,verbs=update

func (r *ClusterMeshReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = ctrl.LoggerFrom(ctx)
	cluster := &clusterv1beta1.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, err
	}

	if _, ok := cluster.Labels["clusterGroup"]; !ok {
		return ctrl.Result{}, nil
	}

	if _, ok := cluster.Annotations["clustermesh.infrastructure.wildlife.io"]; !ok {
		return ctrl.Result{}, nil
	}
	r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))

	defer func() {
		r.log.Info(fmt.Sprintf("finished reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))
	}()

	clustermesh := &clustermeshv1beta1.ClusterMesh{}

	// populate spec
	clSpec, err := r.populateClusterSpec(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	key := client.ObjectKey{
		Name: cluster.Labels["clusterGroup"],
	}

	if err := r.Get(ctx, key, clustermesh); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		ccm := crossplane.NewCrossPlaneClusterMesh(cluster.Labels["clusterGroup"], clSpec)
		r.log.Info(fmt.Sprintf("creating clustermesh %s", ccm.ObjectMeta.GetName()))
		if err := r.Create(ctx, ccm); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	clusterList := clustermesh.Spec.Clusters
	for _, objCluster := range clusterList {
		if cmp.Equal(objCluster, clSpec) {
			return ctrl.Result{}, nil
		}
	}

	clustermesh.Spec.Clusters = append(clustermesh.Spec.Clusters, clSpec)
	r.log.Info(fmt.Sprintf("adding %s to clustermesh %s", cluster.ObjectMeta.Name, clustermesh.Name))
	if err := r.Client.Update(ctx, clustermesh); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ClusterMeshReconciler) populateClusterSpec(ctx context.Context, cluster *clusterv1beta1.Cluster) (*clustermeshv1beta1.ClusterSpec, error) {
	clusterSpec := &clustermeshv1beta1.ClusterSpec{}

	// Fetch KopsControlPlane
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

	clusterSpec.Name = cluster.Name
	clusterSpec.Namespace = cluster.Namespace
	clusterSpec.Region = *region
	clusterSpec.VPCID = *vpcId

	return clusterSpec, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMeshReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1beta1.Cluster{}).
		Complete(r)
}
