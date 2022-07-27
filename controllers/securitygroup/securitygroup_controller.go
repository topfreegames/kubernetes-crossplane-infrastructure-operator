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
	"strconv"
	"time"

	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"
	"github.com/topfreegames/provider-crossplane/pkg/kops"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrSecurityGroupNotAvailable = errors.New("security group not available")

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	log                         logr.Logger
	Recorder                    record.EventRecorder
	NewEC2ClientFactory         func(cfg aws.Config) ec2.EC2Client
	NewAutoScalingClientFactory func(cfg aws.Config) autoscaling.AutoScalingClient
	ManageCrossplaneSGFactory   func(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error
}

//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.wildlife.io,resources=securitygroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kopsmachinepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kopscontrolplanes,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {

	r.log = ctrl.LoggerFrom(ctx)

	securityGroup := &securitygroupv1alpha1.SecurityGroup{}
	if err := r.Get(ctx, req.NamespacedName, securityGroup); err != nil {
		return ctrl.Result{}, err
	}

	if securityGroup.Spec.InfrastructureRef == nil {
		return ctrl.Result{}, fmt.Errorf("infrastructureRef isn't defined")
	}

	r.log.Info(fmt.Sprintf("starting reconcile loop for %s", securityGroup.ObjectMeta.GetName()))

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(securityGroup, r.Client)
	if err != nil {
		r.log.Error(err, "failed to initialize patch helper")
		return ctrl.Result{}, err
	}

	defer func() {

		err = patchHelper.Patch(ctx, securityGroup)
		if err != nil {
			r.log.Error(rerr, "Failed to patch kopsControlPlane")
			if rerr == nil {
				rerr = err
			}
		}
		r.log.Info(fmt.Sprintf("finished reconcile loop for %s", securityGroup.ObjectMeta.GetName()))
	}()

	switch securityGroup.Spec.InfrastructureRef.Kind {
	case "KopsMachinePool":
		err := r.ReconcileKopsMachinePool(ctx, securityGroup)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
	case "KopsControlPlane":
		err := r.ReconcileKopsControlPlane(ctx, securityGroup)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, fmt.Errorf("infrastructureRef not supported")
	}

	securityGroup.Status.Ready = true

	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) attachSGToASG(ctx context.Context, ec2Client ec2.EC2Client, asgClient autoscaling.AutoScalingClient, asgName, sgId string) error {

	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, asgClient, asgName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, ec2Client, *asg.LaunchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	ltVersionOutput, err := ec2.AttachSecurityGroupToLaunchTemplate(ctx, ec2Client, sgId, launchTemplateVersion)
	if err != nil {
		return err
	}

	latestVersion := strconv.FormatInt(*ltVersionOutput.LaunchTemplateVersion.VersionNumber, 10)

	if *asg.LaunchTemplate.Version != latestVersion {
		_, err = autoscaling.UpdateAutoScalingGroupLaunchTemplate(ctx, asgClient, *ltVersionOutput.LaunchTemplateVersion.LaunchTemplateId, latestVersion, asgName)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *SecurityGroupReconciler) ReconcileKopsControlPlane(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup) error {

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

	if region == nil {
		return fmt.Errorf("region not defined")
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

	csg := crossplane.NewCrossplaneSecurityGroup(sg, vpcId, region)
	err = r.ManageCrossplaneSGFactory(ctx, r.Client, csg)
	if err != nil {
		conditions.MarkFalse(sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return err
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		conditions.MarkFalse(sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return err
	}
	conditions.MarkTrue(sg, securitygroupv1alpha1.CrossplaneResourceReadyCondition)

	availableCondition := crossplane.GetSecurityGroupReadyCondition(csg)
	if availableCondition == nil {
		return ErrSecurityGroupNotAvailable
	} else {
		if availableCondition.Status == corev1.ConditionTrue {
			conditions.MarkTrue(sg, securitygroupv1alpha1.SecurityGroupReadyCondition)
		} else {
			conditions.MarkFalse(sg,
				securitygroupv1alpha1.SecurityGroupReadyCondition,
				string(availableCondition.Reason),
				clusterv1beta1.ConditionSeverityError,
				availableCondition.Message,
			)
			return ErrSecurityGroupNotAvailable
		}
	}

	asgName, err := kops.GetAutoScalingGroupNameFromKopsMachinePool(*kmp)
	if err != nil {
		return err
	}

	asgClient := r.NewAutoScalingClientFactory(cfg)
	err = r.attachSGToASG(ctx, ec2Client, asgClient, *asgName, csg.Status.AtProvider.SecurityGroupID)
	if err != nil {
		conditions.MarkFalse(sg,
			securitygroupv1alpha1.SecurityGroupAttachedCondition,
			securitygroupv1alpha1.SecurityGroupAttachmentFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return err
	}
	conditions.MarkTrue(sg, securitygroupv1alpha1.SecurityGroupAttachedCondition)

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitygroupv1alpha1.SecurityGroup{}).
		Complete(r)
}
