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
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	"github.com/topfreegames/provider-crossplane/pkg/aws/autoscaling"
	"github.com/topfreegames/provider-crossplane/pkg/aws/ec2"
	"github.com/topfreegames/provider-crossplane/pkg/crossplane"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	start := time.Now()
	r.log = ctrl.LoggerFrom(ctx)

	sg := &securitygroupv1alpha1.SecurityGroup{}
	if err := r.Get(ctx, req.NamespacedName, sg); err != nil {
		return ctrl.Result{}, err
	}

	if !sg.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, sg)
	}

	if sg.Spec.InfrastructureRef == nil {
		return ctrl.Result{}, fmt.Errorf("infrastructureRef isn't defined")
	}

	r.log.Info(fmt.Sprintf("starting reconcile loop for %s", sg.ObjectMeta.GetName()))

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(sg, r.Client)
	if err != nil {
		r.log.Error(err, "failed to initialize patch helper")
		return ctrl.Result{}, err
	}

	defer func() {

		err = patchHelper.Patch(ctx, sg)
		if err != nil {
			r.log.Error(rerr, "Failed to patch security group", "sgName", sg.Name)
			if rerr == nil {
				rerr = err
			}
		}
		r.log.Info(fmt.Sprintf("finished reconcile loop for %s", sg.ObjectMeta.GetName()))
	}()

	result, err := r.reconcileNormal(ctx, sg)
	durationMsg := fmt.Sprintf("finished reconcile security groups loop for %s finished in %s ", sg.ObjectMeta.Name, time.Since(start).String())
	if result.RequeueAfter > 0 {
		durationMsg = fmt.Sprintf("%s, next run in %s", durationMsg, result.RequeueAfter.String())
	}
	r.log.Info(durationMsg)
	return result, err
}

func (r *SecurityGroupReconciler) reconcileDelete(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup) (ctrl.Result, error) {
	r.log.Info(fmt.Sprintf("reconciling deletion for security group %s\n", sg.Name))

	key := client.ObjectKey{
		Name: sg.Name,
	}

	csg := &crossec2v1beta1.SecurityGroup{}
	err := r.Get(ctx, key, csg)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("could not retrieve security group: %w", err)
	}

	err = r.Delete(ctx, csg)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) reconcileNormal(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup) (ctrl.Result, error) {

	switch sg.Spec.InfrastructureRef.Kind {
	case "KopsMachinePool":
		kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
		key := client.ObjectKey{
			Name:      sg.Spec.InfrastructureRef.Name,
			Namespace: sg.Spec.InfrastructureRef.Namespace,
		}
		if err := r.Client.Get(ctx, key, kmp); err != nil {
			return ctrl.Result{}, err
		}

		err := r.reconcileKopsMachinePool(ctx, sg, kmp)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

	case "KopsControlPlane":
		kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
		key := client.ObjectKey{
			Namespace: sg.Spec.InfrastructureRef.Namespace,
			Name:      sg.Spec.InfrastructureRef.Name,
		}
		if err := r.Client.Get(ctx, key, kcp); err != nil {
			return ctrl.Result{}, err
		}

		err := r.reconcileKopsControlPlane(ctx, sg, kcp)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

	default:
		return ctrl.Result{}, fmt.Errorf("infrastructureRef not supported")
	}

	sg.Status.Ready = true
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) reconcileKopsControlPlane(
	ctx context.Context,
	sg *securitygroupv1alpha1.SecurityGroup,
	kcp *kcontrolplanev1alpha1.KopsControlPlane,
) error {
	vpcId, region, cfg, ec2Client, err := r.getAwsAccountInfo(ctx, kcp)
	if err != nil {
		return err
	}

	csg, err := r.createCrossplaneSecurityGroup(ctx, vpcId, region, sg)
	if err != nil {
		return fmt.Errorf("error creating crossplane securitygroup: %w", err)
	}

	kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", kcp.Name)
	if err != nil {
		return err
	}

	for _, kmp := range kmps {
		asgName, err := kops.GetAutoScalingGroupNameFromKopsMachinePool(kmp)
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
	}
	conditions.MarkTrue(sg, securitygroupv1alpha1.SecurityGroupAttachedCondition)
	conditions.MarkTrue(sg, securitygroupv1alpha1.SecurityGroupReadyCondition)

	return nil
}

func (r *SecurityGroupReconciler) reconcileKopsMachinePool(
	ctx context.Context,
	sg *securitygroupv1alpha1.SecurityGroup,
	kmp *kinfrastructurev1alpha1.KopsMachinePool,
) error {
	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key := client.ObjectKey{
		Namespace: sg.Spec.InfrastructureRef.Namespace,
		Name:      kmp.Spec.ClusterName,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return err
	}

	vpcId, region, cfg, ec2Client, err := r.getAwsAccountInfo(ctx, kcp)
	if err != nil {
		return fmt.Errorf("error retrieving aws account: %w", err)
	}

	csg, err := r.createCrossplaneSecurityGroup(ctx, vpcId, region, sg)
	if err != nil {
		return fmt.Errorf("error creating crossplane securitygroup: %w", err)
	}

	asgName, err := kops.GetAutoScalingGroupNameFromKopsMachinePool(*kmp)
	if err != nil {
		return fmt.Errorf("error retrieving ASG name: %w", err)
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
	conditions.MarkTrue(sg, securitygroupv1alpha1.SecurityGroupReadyCondition)

	return nil
}

func (r *SecurityGroupReconciler) createCrossplaneSecurityGroup(ctx context.Context, vpcId, region *string, sg *securitygroupv1alpha1.SecurityGroup) (*crossec2v1beta1.SecurityGroup, error) {
	csg := crossplane.NewCrossplaneSecurityGroup(sg, vpcId, region)
	err := r.ManageCrossplaneSGFactory(ctx, r.Client, csg)
	if err != nil {
		conditions.MarkFalse(sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return &crossec2v1beta1.SecurityGroup{}, err
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		conditions.MarkFalse(sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return &crossec2v1beta1.SecurityGroup{}, err
	}
	conditions.MarkTrue(sg, securitygroupv1alpha1.CrossplaneResourceReadyCondition)

	availableCondition := crossplane.GetSecurityGroupReadyCondition(csg)
	if availableCondition == nil {
		return &crossec2v1beta1.SecurityGroup{}, ErrSecurityGroupNotAvailable
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
			return &crossec2v1beta1.SecurityGroup{}, ErrSecurityGroupNotAvailable
		}
	}
	return csg, nil
}

func (r *SecurityGroupReconciler) getAwsAccountInfo(ctx context.Context, kcp *kcontrolplanev1alpha1.KopsControlPlane) (*string, *string, aws.Config, ec2.EC2Client, error) {
	subnet, err := kops.GetSubnetFromKopsControlPlane(kcp)
	if err != nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, err
	}

	if region == nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, fmt.Errorf("region not defined")
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(*region),
	)
	if err != nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, err
	}

	ec2Client := r.NewEC2ClientFactory(cfg)

	vpcId, err := ec2.GetVPCIdFromCIDR(ctx, ec2Client, kcp.Spec.KopsClusterSpec.NetworkCIDR)
	if err != nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, err
	}

	if vpcId == nil {
		return aws.String(""), aws.String(""), aws.Config{}, nil, fmt.Errorf("vpcId not defined")
	}

	return vpcId, region, cfg, ec2Client, nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitygroupv1alpha1.SecurityGroup{}).
		Complete(r)
}
