/*
Copyright 2022.

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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/go-logr/logr"

	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	"github.com/topfreegames/provider-crossplane/pkg/kops"

	"k8s.io/apimachinery/pkg/runtime"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrSecurityGroupIDNotFound = errors.New("security group ID still not found")

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	log                         logr.Logger
	NewEC2ClientFactory         func(cfg aws.Config) ec2.EC2Client
	NewAutoScalingClientFactory func(cfg aws.Config) autoscaling.AutoScalingClient
}

func (r *SecurityGroupReconciler) attachSGToKopsMachinePool(ctx context.Context, ec2Client ec2.EC2Client, cfg aws.Config, asgName, sgId string) error {

	asgClient := r.NewAutoScalingClientFactory(cfg)
	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, asgClient, asgName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, ec2Client, *asg.LaunchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	for _, sgID := range launchTemplateVersion.LaunchTemplateData.SecurityGroupIds {
		if sgID == sgId {
			return nil
		}
	}

	_, err = ec2.AttachSecurityGroupToLaunchTemplate(ctx, ec2Client, sgId, launchTemplateVersion)
	if err != nil {
		return err
	}

	_, err = ec2.UpdateLaunchTemplateDefaultVersion(ctx, ec2Client, *launchTemplateVersion.LaunchTemplateId)
	if err != nil {
		return err
	}

	return nil
}

func (r *SecurityGroupReconciler) ReconcileKopsMachinePool(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup) error {

	// Fetch KopsMachinePool
	kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
	key := client.ObjectKey{
		Name:      sg.Spec.InfrastructureRef.Name,
		Namespace: sg.Spec.InfrastructureRef.Namespace,
	}
	if err := r.Client.Get(ctx, key, kmp); err != nil {
		return err
	}

	// Fetch Cluster
	cluster := &clusterv1beta1.Cluster{}
	key = client.ObjectKey{
		Namespace: kmp.ObjectMeta.Namespace,
		Name:      kmp.Spec.ClusterName,
	}
	if err := r.Client.Get(ctx, key, cluster); err != nil {
		return err
	}

	// Fetch KopsControlPlane
	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key = client.ObjectKey{
		Namespace: cluster.Spec.ControlPlaneRef.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return err
	}

	subnet, err := kops.GetSubnetFromKopsControlPlane(kcp)
	if err != nil {
		return err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return err
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(*region),
	)
	if err != nil {
		return err
	}

	ec2Client := r.NewEC2ClientFactory(cfg)

	vpcId, err := ec2.GetVPCIdFromCIDR(ctx, ec2Client, kcp.Spec.KopsClusterSpec.NetworkCIDR)
	if err != nil {
		return err
	}

	if vpcId == nil {
		return fmt.Errorf("vpcId not defined")
	}

	if region == nil {
		return fmt.Errorf("region not defined")
	}

	csg := crossplane.NewCrossplaneSecurityGroup(ctx, sg, vpcId, region)
	err = crossplane.ManageCrossplaneSecurityGroupResource(ctx, r.Client, csg)
	if err != nil {
		return err
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		return err
	}

	if csg.Status.AtProvider.SecurityGroupID == "" {
		return ErrSecurityGroupIDNotFound
	}

	asgName, err := kops.GetAutoScalingGroupNameFromKopsMachinePool(*kmp)
	if err != nil {
		return err
	}

	err = r.attachSGToKopsMachinePool(ctx, ec2Client, cfg, *asgName, csg.Status.AtProvider.SecurityGroupID)
	if err != nil {
		return err
	}

	return nil
}

//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kopsmachinepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kopscontrolplanes,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	r.log = ctrl.LoggerFrom(ctx)

	securityGroup := &securitygroupv1alpha1.SecurityGroup{}
	if err := r.Get(ctx, req.NamespacedName, securityGroup); err != nil {
		return ctrl.Result{}, err
	}

	r.log.Info(fmt.Sprintf("starting reconcile loop for %s", securityGroup.ObjectMeta.GetName()))

	defer func() {
		r.log.Info(fmt.Sprintf("finished reconcile loop for %s", securityGroup.ObjectMeta.GetName()))
	}()

	if securityGroup.Spec.InfrastructureRef == nil {
		return ctrl.Result{}, fmt.Errorf("infrastructureRef isn't defined")
	}

	// Only supports KopsMachinePool for now
	switch securityGroup.Spec.InfrastructureRef.Kind {
	case "KopsMachinePool":
		err := r.ReconcileKopsMachinePool(ctx, securityGroup)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupIDNotFound) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, fmt.Errorf("infrastructureRef not supported")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitygroupv1alpha1.SecurityGroup{}).
		Complete(r)
}
