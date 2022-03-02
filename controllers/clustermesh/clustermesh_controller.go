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

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	clustermeshv1beta1 "github.com/topfreegames/provider-crossplane/apis/clustermesh/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterMeshReconciler reconciles a ClusterMesh object
type ClusterMeshReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	log    logr.Logger
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
	r.log.Info(fmt.Sprintf("starting reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))

	defer func() {
		r.log.Info(fmt.Sprintf("finished reconcile clustermesh loop for %s", cluster.ObjectMeta.Name))
	}()

	if _, ok := cluster.Annotations["clustermesh.infrastructure.wildlife.io"]; !ok {
		return ctrl.Result{}, nil
	}

	clustermesh := &clustermeshv1beta1.ClusterMesh{}
	namespacedName := types.NamespacedName{
		Namespace: "crossplane-system",
		Name:      cluster.Labels["clusterGroup"],
	}
	clusterRefList := []*v1.ObjectReference{}
	clusterRef := &v1.ObjectReference{
		APIVersion: cluster.TypeMeta.APIVersion,
		Kind:       cluster.TypeMeta.Kind,
		Name:       cluster.ObjectMeta.Name,
		Namespace:  cluster.ObjectMeta.Namespace,
	}
	clusterRefList = append(clusterRefList, clusterRef)
	if err := r.Get(ctx, namespacedName, clustermesh); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		ccm := crossplane.NewCrossPlaneClusterMesh(ctx, namespacedName, cluster, clusterRefList)
		r.log.Info(fmt.Sprintf("creating clustermesh %s", ccm.ObjectMeta.GetName()))
		if err := r.Create(ctx, ccm); err != nil {
			return ctrl.Result{}, err
		} else {
			return ctrl.Result{}, nil
		}
	}

	objectClusterRefList := clustermesh.Spec.ClusterRefList
	for _, objClusterRef := range objectClusterRefList {
		if cmp.Equal(objClusterRef, clusterRef) {
			return ctrl.Result{}, nil
		}
	}

	clustermesh.Spec.ClusterRefList = append(clustermesh.Spec.ClusterRefList, clusterRef)
	r.log.Info(fmt.Sprintf("adding %s to clustermesh %s", cluster.ObjectMeta.Name, clustermesh.Name))
	if err := r.Client.Update(ctx, clustermesh); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterMeshReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&clusterv1beta1.Cluster{}).
		Complete(r)
}
