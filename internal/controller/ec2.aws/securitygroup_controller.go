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

package sgcontroller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/google/go-cmp/cmp"
	securitygroupv1alpha2 "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/api/ec2.aws/v1alpha2"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/autoscaling"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/aws/ec2"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/crossplane"
	kopsutils "github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/kops"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	"github.com/topfreegames/kubernetes-kops-operator/pkg/kops"

	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	"github.com/topfreegames/kubernetes-crossplane-infrastructure-operator/pkg/spot"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2service "github.com/aws/aws-sdk-go-v2/service/ec2"
	crossec2v1beta1 "github.com/crossplane-contrib/provider-aws/apis/ec2/v1beta1"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/strings/slices"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	requeue30seconds             = ctrl.Result{RequeueAfter: 30 * time.Second}
	resultDefault                = ctrl.Result{RequeueAfter: 1 * time.Hour}
	resultError                  = ctrl.Result{RequeueAfter: 30 * time.Minute}
	ErrSecurityGroupNotAvailable = errors.New("security group not available")
)

const (
	securityGroupFinalizer = "securitygroup.wildlife.infrastructure.io"

	AnnotationKeyReconciliationPaused = "crossplane.io/paused"
)

func getFinalizerName(sgName string) string {
	return securityGroupFinalizer + "/" + sgName
}

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	client.Client
	Scheme                          *runtime.Scheme
	Recorder                        record.EventRecorder
	NewEC2ClientFactory             func(cfg aws.Config) ec2.EC2Client
	NewAutoScalingClientFactory     func(cfg aws.Config) autoscaling.AutoScalingClient
	NewOceanCloudProviderAWSFactory func() spot.OceanClient
}

type SecurityGroupReconciliation struct {
	SecurityGroupReconciler
	log                logr.Logger
	start              time.Time
	ec2Client          ec2.EC2Client
	asgClient          autoscaling.AutoScalingClient
	sg                 *securitygroupv1alpha2.SecurityGroup
	region             *string
	vpcId              *string
	providerConfigName string
}

func DefaultReconciler(mgr manager.Manager) *SecurityGroupReconciler {
	return &SecurityGroupReconciler{
		Client:                          mgr.GetClient(),
		Scheme:                          mgr.GetScheme(),
		Recorder:                        mgr.GetEventRecorderFor("securityGroup-controller"),
		NewEC2ClientFactory:             ec2.NewEC2Client,
		NewAutoScalingClientFactory:     autoscaling.NewAutoScalingClient,
		NewOceanCloudProviderAWSFactory: spot.NewOceanCloudProviderAWS,
	}
}

//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kopsmachinepools,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kopscontrolplanes,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update

func (c *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {
	r := &SecurityGroupReconciliation{
		SecurityGroupReconciler: *c,
		start:                   time.Now(),
		log:                     ctrl.LoggerFrom(ctx),
	}

	r.sg = &securitygroupv1alpha2.SecurityGroup{}
	if err := r.Get(ctx, req.NamespacedName, r.sg); err != nil {
		return resultError, client.IgnoreNotFound(err)
	}

	if r.sg.Spec.InfrastructureRef == nil {
		return resultDefault, fmt.Errorf("infrastructureRef not supported")
	}

	if r.sg.GetAnnotations()[AnnotationKeyReconciliationPaused] == "true" {
		r.log.Info("Reconciliation is paused via the pause annotation", "annotation", AnnotationKeyReconciliationPaused, "value", "true")
		r.Recorder.Eventf(r.sg, corev1.EventTypeNormal, securitygroupv1alpha2.ReasonReconcilePaused, "Reconciliation is paused via the pause annotation")
		return resultDefault, nil
	}

	r.log.Info(fmt.Sprintf("starting reconcile loop for %s", r.sg.ObjectMeta.GetName()))

	defer func() {
		securityGroupHelper := r.sg.DeepCopy()
		if err := r.Update(ctx, r.sg); err != nil {
			r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdate", "failed to update security group %s: %s", r.sg.Name, err)
			if rerr == nil {
				rerr = err
			}
		}

		if securityGroupHelper.ObjectMeta.DeletionTimestamp.IsZero() {
			r.sg.Status = securityGroupHelper.Status
			if err := r.Status().Update(ctx, r.sg); err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdateStatus", "failed to update security group status %s: %s", r.sg.Name, err)
				if rerr == nil {
					rerr = err
				}
			}
		}

		r.log.Info(fmt.Sprintf("finished reconcile loop for %s", r.sg.ObjectMeta.GetName()))
	}()

	err := r.retrieveInfraRefInfo(ctx)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("could not retrieve infra info from any of refs of security group %v", r.sg))
		return resultError, err
	}

	if !r.sg.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, r.sg)
	}

	return r.reconcileNormal(ctx)
}

func (r *SecurityGroupReconciliation) retrieveInfraRefInfo(ctx context.Context) error {
	if len(r.sg.Spec.InfrastructureRef) == 0 {
		return fmt.Errorf("no infrastructureRef found")
	}
	var retrieveErr error

	for _, infrastructureRef := range r.sg.Spec.InfrastructureRef {
		switch infrastructureRef.Kind {
		case "KopsMachinePool":
			kmp := kinfrastructurev1alpha1.KopsMachinePool{}
			key := client.ObjectKey{
				Name:      infrastructureRef.Name,
				Namespace: infrastructureRef.Namespace,
			}
			if err := r.Client.Get(ctx, key, &kmp); err != nil {
				retrieveErr = err
				continue
			}

			kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
			key = client.ObjectKey{
				Name:      kmp.Spec.ClusterName,
				Namespace: kmp.ObjectMeta.Namespace,
			}
			if err := r.Client.Get(ctx, key, kcp); err != nil {
				retrieveErr = err
				continue
			}

			region, err := kopsutils.GetRegionFromKopsControlPlane(ctx, kcp)
			if err != nil {
				retrieveErr = fmt.Errorf("error retrieving region: %w", err)
				continue
			}

			providerConfigName, awsCfg, err := kopsutils.RetrieveAWSCredentialsFromKCP(ctx, r.Client, region, kcp)
			if err != nil {
				retrieveErr = err
				continue
			}

			r.ec2Client = r.NewEC2ClientFactory(*awsCfg)
			r.asgClient = r.NewAutoScalingClientFactory(*awsCfg)

			vpcId, err := ec2.GetVPCIdWithCIDRAndClusterName(ctx, r.ec2Client, kcp.Name, kcp.Spec.KopsClusterSpec.Networking.NetworkCIDR)
			if err != nil {
				retrieveErr = fmt.Errorf("error retrieving vpcID: %w", err)
				continue
			}

			r.providerConfigName = providerConfigName
			r.region = region
			r.vpcId = vpcId

			return nil
		case "KopsControlPlane":
			kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
			key := client.ObjectKey{
				Name:      infrastructureRef.Name,
				Namespace: infrastructureRef.Namespace,
			}
			if err := r.Client.Get(ctx, key, kcp); err != nil {
				retrieveErr = err
				continue
			}

			region, err := kopsutils.GetRegionFromKopsControlPlane(ctx, kcp)
			if err != nil {
				retrieveErr = fmt.Errorf("error retrieving region: %w", err)
				continue
			}

			providerConfigName, awsCfg, err := kopsutils.RetrieveAWSCredentialsFromKCP(ctx, r.Client, region, kcp)
			if err != nil {
				retrieveErr = err
				continue
			}

			r.ec2Client = r.NewEC2ClientFactory(*awsCfg)
			r.asgClient = r.NewAutoScalingClientFactory(*awsCfg)

			vpcId, err := ec2.GetVPCIdWithCIDRAndClusterName(ctx, r.ec2Client, kcp.Name, kcp.Spec.KopsClusterSpec.Networking.NetworkCIDR)
			if err != nil {
				retrieveErr = fmt.Errorf("error retrieving vpcID: %w", err)
				continue
			}

			r.providerConfigName = providerConfigName
			r.region = region
			r.vpcId = vpcId

			return nil
		default:
			retrieveErr = fmt.Errorf("infrastructureRef not supported")
			continue
		}
	}

	return retrieveErr
}

func (r *SecurityGroupReconciliation) reconcileNormal(ctx context.Context) (ctrl.Result, error) {

	if !controllerutil.ContainsFinalizer(r.sg, securityGroupFinalizer) {
		controllerutil.AddFinalizer(r.sg, securityGroupFinalizer)
	}

	csg, err := crossplane.CreateOrUpdateCrossplaneSecurityGroup(ctx, r.Client, r.vpcId, r.region, r.providerConfigName, r.sg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha2.CrossplaneResourceReadyCondition,
			securitygroupv1alpha2.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return resultError, fmt.Errorf("error creating crossplane securitygroup: %w", err)
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha2.CrossplaneResourceReadyCondition,
			securitygroupv1alpha2.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return resultError, err
	}
	conditions.MarkTrue(r.sg, securitygroupv1alpha2.CrossplaneResourceReadyCondition)

	availableCondition := crossplane.GetSecurityGroupReadyCondition(csg)
	if availableCondition == nil {
		return resultError, ErrSecurityGroupNotAvailable
	} else {
		if availableCondition.Status == corev1.ConditionTrue {
			conditions.MarkTrue(r.sg, securitygroupv1alpha2.SecurityGroupReadyCondition)
		} else {
			conditions.MarkFalse(r.sg,
				securitygroupv1alpha2.SecurityGroupReadyCondition,
				string(availableCondition.Reason),
				clusterv1beta1.ConditionSeverityError,
				availableCondition.Message,
			)
			return resultError, ErrSecurityGroupNotAvailable
		}
	}

	result, err := r.ensureAttachReferences(ctx, csg)
	if err != nil {
		return result, err
	}

	result, err = r.ensureDetachRemovedReferences(ctx, csg)
	if err != nil {
		return result, err
	}

	r.sg.Status.Ready = true
	r.sg.Status.AppliedInfrastructureRef = r.sg.DeepCopy().Spec.InfrastructureRef
	return resultDefault, nil
}

func (r *SecurityGroupReconciliation) attachKopsMachinePool(ctx context.Context, csg *crossec2v1beta1.SecurityGroup, kcp *kcontrolplanev1alpha1.KopsControlPlane, kmps ...kinfrastructurev1alpha1.KopsMachinePool) error {
	var attachErr error
	for _, kmp := range kmps {
		if len(kmp.Spec.SpotInstOptions) != 0 {
			oceanClient := r.NewOceanCloudProviderAWSFactory()
			launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, kmp.Spec.ClusterName)
			if err != nil {
				return err
			}
			err = r.attachSGToVNG(ctx, oceanClient, launchSpecs, kmp.Name, csg)
			if err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha2.SecurityGroupAttachmentFailedReason, err.Error())
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		} else if kmp.Spec.KopsInstanceGroupSpec.Manager == "Karpenter" {
			launchTemplateName, err := kops.GetCloudResourceNameFromKopsMachinePool(kmp)
			if err != nil {
				attachErr = multierror.Append(attachErr, err)
				continue
			}

			err = r.attachSGToLaunchTemplate(ctx, kcp.Name, launchTemplateName, csg.Status.AtProvider.SecurityGroupID)
			if err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha2.SecurityGroupAttachmentFailedReason, err.Error())
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		} else {
			asgName, err := kops.GetCloudResourceNameFromKopsMachinePool(kmp)
			if err != nil {
				attachErr = multierror.Append(attachErr, err)
				continue
			}

			err = r.attachSGToASG(ctx, asgName, csg.Status.AtProvider.SecurityGroupID)
			if err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha2.SecurityGroupAttachmentFailedReason, err.Error())
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		}
	}
	if attachErr != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha2.SecurityGroupAttachedCondition,
			securitygroupv1alpha2.SecurityGroupAttachmentFailedReason,
			clusterv1beta1.ConditionSeverityError,
			attachErr.Error(),
		)
	} else {
		conditions.MarkTrue(r.sg, securitygroupv1alpha2.SecurityGroupAttachedCondition)
		conditions.MarkTrue(r.sg, securitygroupv1alpha2.SecurityGroupReadyCondition)
	}

	return attachErr
}

func (r *SecurityGroupReconciliation) attachSGToVNG(ctx context.Context, oceanClient spot.OceanClient, launchSpecs []*oceanaws.LaunchSpec, kmpName string, csg *crossec2v1beta1.SecurityGroup) error {
	for _, vng := range launchSpecs {
		for _, labels := range vng.Labels {
			if *labels.Key == "kops.k8s.io/instance-group-name" && *labels.Value == kmpName {
				currentSecurityGroups := vng.SecurityGroupIDs
				sort.Strings(currentSecurityGroups)
				sgIds := []string{}
				for _, sgId := range currentSecurityGroups {
					ok, err := ec2.CheckSecurityGroupExists(ctx, r.ec2Client, sgId)
					if err != nil {
						return err
					}
					if ok && sgId != csg.Status.AtProvider.SecurityGroupID {
						sgIds = append(sgIds, sgId)
					}
				}
				sgIds = append(sgIds, csg.Status.AtProvider.SecurityGroupID)
				sort.Strings(sgIds)
				if !cmp.Equal(sgIds, currentSecurityGroups) {
					vng.SecurityGroupIDs = sgIds
					vng.SetSecurityGroupIDs(vng.SecurityGroupIDs)
					// We need to clean these values because of the spot update API
					vng.CreatedAt = nil
					vng.OceanID = nil
					vng.UpdatedAt = nil
					_, err := oceanClient.UpdateLaunchSpec(ctx, &oceanaws.UpdateLaunchSpecInput{
						LaunchSpec: vng,
					})
					if err != nil {
						return fmt.Errorf("error updating ocean cluster launch spec: %w", err)
					}
				}

				outputDescribeInstances, err := r.ec2Client.DescribeInstances(ctx, &ec2service.DescribeInstancesInput{
					Filters: []ec2types.Filter{
						{
							Name:   aws.String("tag:spotinst:ocean:launchspec:name"),
							Values: []string{*vng.Name},
						},
						{
							Name:   aws.String("tag:spotinst:aws:ec2:group:createdBy"),
							Values: []string{"spotinst"},
						},
						{
							Name:   aws.String("instance-state-name"),
							Values: []string{"running"},
						},
					},
				})
				if err != nil {
					return err
				}

				instanceIDs := []string{}
				for _, reservation := range outputDescribeInstances.Reservations {
					for _, instance := range reservation.Instances {
						instanceIDs = append(instanceIDs, *instance.InstanceId)
					}
				}

				err = ec2.AttachSecurityGroupToInstances(ctx, r.ec2Client, instanceIDs, csg.Status.AtProvider.SecurityGroupID)
				if err != nil {
					r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "SecurityGroupInstancesAttachmentFailed", err.Error())
				}

				return nil
			}
		}
	}
	return fmt.Errorf("error no vng found with instance group name: %s", kmpName)
}

func (r *SecurityGroupReconciliation) attachSGToASG(ctx context.Context, asgName, sgID string) error {

	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, r.asgClient, asgName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, r.ec2Client, *asg.LaunchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	ltVersionOutput, err := ec2.AttachSecurityGroupToLaunchTemplate(ctx, r.ec2Client, sgID, launchTemplateVersion)
	if err != nil {
		return err
	}

	latestVersion := strconv.FormatInt(*ltVersionOutput.LaunchTemplateVersion.VersionNumber, 10)

	if *asg.LaunchTemplate.Version != latestVersion {
		_, err = autoscaling.UpdateAutoScalingGroupLaunchTemplate(ctx, r.asgClient, *ltVersionOutput.LaunchTemplateVersion.LaunchTemplateId, latestVersion, asgName)
		if err != nil {
			return err
		}
	}

	instanceIDs := []string{}
	for _, instance := range asg.Instances {
		instanceIDs = append(instanceIDs, *instance.InstanceId)
	}

	err = ec2.AttachSecurityGroupToInstances(ctx, r.ec2Client, instanceIDs, sgID)
	if err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "SecurityGroupInstancesAttachmentFailed", err.Error())
	}

	return nil
}

func (r *SecurityGroupReconciliation) attachSGToLaunchTemplate(ctx context.Context, kubernetesClusterName, launchTemplateName, sgID string) error {

	launchTemplate, err := ec2.GetLaunchTemplateFromInstanceGroup(ctx, r.ec2Client, kubernetesClusterName, launchTemplateName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, r.ec2Client, *launchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	_, err = ec2.AttachSecurityGroupToLaunchTemplate(ctx, r.ec2Client, sgID, launchTemplateVersion)
	if err != nil {
		return err
	}

	outputDescribeInstances, err := r.ec2Client.DescribeInstances(ctx, &ec2service.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:aws:ec2launchtemplate:id"),
				Values: []string{*launchTemplate.LaunchTemplateId},
			},
		},
	})
	if err != nil {
		return err
	}

	instanceIDs := []string{}
	for _, reservation := range outputDescribeInstances.Reservations {
		for _, instance := range reservation.Instances {
			instanceIDs = append(instanceIDs, *instance.InstanceId)
		}
	}

	err = ec2.AttachSecurityGroupToInstances(ctx, r.ec2Client, instanceIDs, sgID)
	if err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "SecurityGroupInstancesAttachmentFailed", err.Error())
		return err
	}

	return nil
}

func (r *SecurityGroupReconciliation) reconcileDelete(ctx context.Context, sg *securitygroupv1alpha2.SecurityGroup) (ctrl.Result, error) {
	r.log.Info(fmt.Sprintf("reconciling deletion for security group %s\n", sg.Name))

	key := client.ObjectKey{
		Name: sg.Name,
	}

	csg := &crossec2v1beta1.SecurityGroup{}
	err := r.Get(ctx, key, csg)
	if apierrors.IsNotFound(err) {
		controllerutil.RemoveFinalizer(sg, securityGroupFinalizer)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not retrieve crossplane security group: %w", err)
	}

	for _, infrastructureRef := range sg.Spec.InfrastructureRef {
		switch infrastructureRef.Kind {
		case "KopsMachinePool":
			kmp := kinfrastructurev1alpha1.KopsMachinePool{}
			key := client.ObjectKey{
				Name:      infrastructureRef.Name,
				Namespace: infrastructureRef.Namespace,
			}
			if err := r.Client.Get(ctx, key, &kmp); err != nil {
				r.log.Error(err, fmt.Sprintf("could not get kmp %v at %v", key.Name, key.Namespace))
				continue
			}

			err := r.detachSGFromKopsMachinePool(ctx, csg, kmp)
			if err != nil {
				r.log.Error(err, fmt.Sprintf("failed to detach sg %v from kmp %v at %v", r.sg.Name, key.Name, key.Namespace))
				continue
			}
			if controllerutil.ContainsFinalizer(&kmp, getFinalizerName(r.sg.Name)) {
				r.removeKMPFinalizer(ctx, kmp)
			}
		case "KopsControlPlane":
			kcp := kcontrolplanev1alpha1.KopsControlPlane{}
			key := client.ObjectKey{
				Namespace: infrastructureRef.Namespace,
				Name:      infrastructureRef.Name,
			}
			if err := r.Client.Get(ctx, key, &kcp); err != nil {
				r.log.Error(err, fmt.Sprintf("could not get kcp %v at %v", key.Name, key.Namespace))
				continue
			}

			kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", kcp.Name)
			if err != nil {
				r.log.Error(err, fmt.Sprintf("could not get kmps of %v", kcp.Name))
				continue
			}

			err = r.detachSGFromKopsMachinePool(ctx, csg, kmps...)
			if err != nil {
				r.log.Error(err, fmt.Sprintf("failed to detach sg %v from kmps of %v", r.sg.Name, kcp.Name))
			}

			r.removeKCPFinalizer(ctx, kcp, kmps...)
		default:
			return resultError, fmt.Errorf("infrastructureRef not supported")
		}
	}

	err = r.Delete(ctx, csg)
	if err != nil {
		return ctrl.Result{}, err
	}
	r.log.Info(fmt.Sprintf("Finished deletion of Crossplane SecurityGroup %s\n", csg.Name))

	controllerutil.RemoveFinalizer(sg, securityGroupFinalizer)
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciliation) detachSGFromKopsMachinePool(ctx context.Context, csg *crossec2v1beta1.SecurityGroup, kmps ...kinfrastructurev1alpha1.KopsMachinePool) error {

	var detachErr error
	for _, kmp := range kmps {
		if len(kmp.Spec.SpotInstOptions) != 0 {
			oceanClient := r.NewOceanCloudProviderAWSFactory()
			launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, kmp.Spec.ClusterName)
			if err != nil {
				return err
			}
			err = r.detachSGFromVNG(ctx, oceanClient, launchSpecs, kmp.Name, csg)
			if err != nil {
				detachErr = multierror.Append(detachErr, err)
				continue
			}
		} else if kmp.Spec.KopsInstanceGroupSpec.Manager == "Karpenter" {
			launchTemplateName, err := kops.GetCloudResourceNameFromKopsMachinePool(kmp)
			if err != nil {
				detachErr = multierror.Append(detachErr, err)
				continue
			}

			err = r.detachSGFromLaunchTemplate(ctx, kmp.Spec.ClusterName, launchTemplateName, csg.Status.AtProvider.SecurityGroupID)
			if err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha2.SecurityGroupAttachmentFailedReason, err.Error())
				detachErr = multierror.Append(detachErr, err)
				continue
			}
		} else {
			asgName, err := kops.GetCloudResourceNameFromKopsMachinePool(kmp)
			if err != nil {
				detachErr = multierror.Append(detachErr, err)
				continue
			}

			err = r.detachSGFromASG(ctx, asgName, csg.Status.AtProvider.SecurityGroupID)
			if err != nil {
				detachErr = multierror.Append(detachErr, err)
				continue
			}
		}
	}

	return detachErr
}

func (r *SecurityGroupReconciliation) detachSGFromVNG(ctx context.Context, oceanClient spot.OceanClient, launchSpecs []*oceanaws.LaunchSpec, kmpName string, csg *crossec2v1beta1.SecurityGroup) error {
	for _, vng := range launchSpecs {
		for _, labels := range vng.Labels {
			if *labels.Key == "kops.k8s.io/instance-group-name" && *labels.Value == kmpName {
				securityGroupsIDs := vng.SecurityGroupIDs
				if !slices.Contains(securityGroupsIDs, csg.Status.AtProvider.SecurityGroupID) {
					return nil
				}
				index := slices.Index(securityGroupsIDs, csg.Status.AtProvider.SecurityGroupID)
				securityGroupsIDs = append(securityGroupsIDs[:index], securityGroupsIDs[index+1:]...)
				vng.SetSecurityGroupIDs(securityGroupsIDs)
				// We need to clean these values because of the spot update API
				vng.CreatedAt = nil
				vng.OceanID = nil
				vng.UpdatedAt = nil
				_, err := oceanClient.UpdateLaunchSpec(ctx, &oceanaws.UpdateLaunchSpecInput{
					LaunchSpec: vng,
				})
				if err != nil {
					return fmt.Errorf("error updating ocean cluster launch spec: %w", err)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("error no vng found with instance group name: %s", kmpName)
}

func (r *SecurityGroupReconciliation) detachSGFromASG(ctx context.Context, asgName, sgId string) error {
	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, r.asgClient, asgName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, r.ec2Client, *asg.LaunchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	ltVersionOutput, err := ec2.DetachSecurityGroupFromLaunchTemplate(ctx, r.ec2Client, sgId, launchTemplateVersion)
	if err != nil {
		return err
	}

	latestVersion := strconv.FormatInt(*ltVersionOutput.LaunchTemplateVersion.VersionNumber, 10)

	if *asg.LaunchTemplate.Version != latestVersion {
		_, err = autoscaling.UpdateAutoScalingGroupLaunchTemplate(ctx, r.asgClient, *ltVersionOutput.LaunchTemplateVersion.LaunchTemplateId, latestVersion, asgName)
		if err != nil {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitygroupv1alpha2.SecurityGroup{}).
		Complete(r)
}

func (r *SecurityGroupReconciliation) detachSGFromLaunchTemplate(ctx context.Context, kubernetesClusterName, launchTemplateName, sgID string) error {

	launchTemplate, err := ec2.GetLaunchTemplateFromInstanceGroup(ctx, r.ec2Client, kubernetesClusterName, launchTemplateName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, r.ec2Client, *launchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	_, err = ec2.DetachSecurityGroupFromLaunchTemplate(ctx, r.ec2Client, sgID, launchTemplateVersion)
	if err != nil {
		return err
	}

	return nil
}

func (r *SecurityGroupReconciliation) ensureAttachReferences(ctx context.Context, csg *crossec2v1beta1.SecurityGroup) (ctrl.Result, error) {
	for _, infrastructureRef := range r.sg.Spec.InfrastructureRef {
		switch infrastructureRef.Kind {
		case "KopsMachinePool":
			kmp := kinfrastructurev1alpha1.KopsMachinePool{}
			key := client.ObjectKey{
				Name:      infrastructureRef.Name,
				Namespace: infrastructureRef.Namespace,
			}
			if err := r.Client.Get(ctx, key, &kmp); err != nil {
				return resultError, err
			}

			kcp := kcontrolplanev1alpha1.KopsControlPlane{}
			key = client.ObjectKey{
				Name:      kmp.Spec.ClusterName,
				Namespace: kmp.ObjectMeta.Namespace,
			}
			if err := r.Client.Get(ctx, key, &kcp); err != nil {
				return resultError, err
			}
			err := r.attachKopsMachinePool(ctx, csg, &kcp, kmp)
			if err != nil {
				if errors.Is(err, ErrSecurityGroupNotAvailable) {
					return requeue30seconds, nil
				}
				return resultError, err
			}
			if !controllerutil.ContainsFinalizer(&kmp, getFinalizerName(r.sg.Name)) {
				r.addKMPFinalizer(ctx, kmp)
			}
		case "KopsControlPlane":
			kcp := kcontrolplanev1alpha1.KopsControlPlane{}
			key := client.ObjectKey{
				Name:      infrastructureRef.Name,
				Namespace: infrastructureRef.Namespace,
			}
			if err := r.Client.Get(ctx, key, &kcp); err != nil {
				return resultError, err
			}

			kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", kcp.Name)
			if err != nil {
				return resultError, err
			}
			err = r.attachKopsMachinePool(ctx, csg, &kcp, kmps...)
			if err != nil {
				if errors.Is(err, ErrSecurityGroupNotAvailable) {
					return requeue30seconds, nil
				}
				return resultError, err
			}
			if !controllerutil.ContainsFinalizer(&kcp, getFinalizerName(r.sg.Name)) {
				r.addKCPFinalizer(ctx, kcp, kmps...)
			}
		default:
			return resultError, fmt.Errorf("infrastructureRef not supported")
		}
	}
	return reconcile.Result{}, nil
}

func (r *SecurityGroupReconciliation) ensureDetachRemovedReferences(ctx context.Context, csg *crossec2v1beta1.SecurityGroup) (ctrl.Result, error) {
	infraRefMap := map[string]*corev1.ObjectReference{}

	for _, ref := range r.sg.Spec.InfrastructureRef {
		infraRefMap[ref.Name] = ref
	}
	for _, ref := range r.sg.Status.AppliedInfrastructureRef {
		if _, ok := infraRefMap[ref.Name]; !ok {
			switch ref.Kind {
			case "KopsMachinePool":

				kmp := kinfrastructurev1alpha1.KopsMachinePool{}
				key := client.ObjectKey{
					Name:      ref.Name,
					Namespace: ref.Namespace,
				}
				if err := r.Client.Get(ctx, key, &kmp); err != nil {
					return resultError, err
				}
				err := r.detachSGFromKopsMachinePool(ctx, csg, kmp)
				if err != nil {
					return resultError, err
				}
				r.removeKMPFinalizer(ctx, kmp)
			case "KopsControlPlane":
				kcp := kcontrolplanev1alpha1.KopsControlPlane{}
				key := client.ObjectKey{
					Namespace: ref.Namespace,
					Name:      ref.Name,
				}
				if err := r.Client.Get(ctx, key, &kcp); err != nil {
					return resultError, err
				}

				kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", kcp.Name)
				if err != nil {
					return resultError, err
				}

				err = r.detachSGFromKopsMachinePool(ctx, csg, kmps...)
				if err != nil {
					return resultError, err
				}
				r.removeKCPFinalizer(ctx, kcp, kmps...)
			default:
				continue
			}
		}
	}
	return resultDefault, nil
}

func (r *SecurityGroupReconciliation) removeKMPFinalizer(ctx context.Context, kmp kinfrastructurev1alpha1.KopsMachinePool) {
	controllerutil.RemoveFinalizer(&kmp, getFinalizerName(r.sg.Name))
	if err := r.Update(ctx, &kmp); err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdate", "failed to remove finalizer in %s: %s", kmp.Name, err)
	}
}

func (r *SecurityGroupReconciliation) removeKCPFinalizer(ctx context.Context, kcp kcontrolplanev1alpha1.KopsControlPlane, kmps ...kinfrastructurev1alpha1.KopsMachinePool) {
	controllerutil.RemoveFinalizer(&kcp, getFinalizerName(r.sg.Name))
	if err := r.Update(ctx, &kcp); err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdate", "failed to remove finalizer in %s: %s", kcp.Name, err)
	}
	for _, kmp := range kmps {
		if controllerutil.ContainsFinalizer(&kmp, getFinalizerName(r.sg.Name)) {
			r.removeKMPFinalizer(ctx, kmp)
		}
	}
}

func (r *SecurityGroupReconciliation) addKMPFinalizer(ctx context.Context, kmp kinfrastructurev1alpha1.KopsMachinePool) {
	controllerutil.AddFinalizer(&kmp, getFinalizerName(r.sg.Name))
	if err := r.Update(ctx, &kmp); err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdate", "failed to add finalizer in %s: %s", kmp.Name, err)
	}
}

func (r *SecurityGroupReconciliation) addKCPFinalizer(ctx context.Context, kcp kcontrolplanev1alpha1.KopsControlPlane, kmps ...kinfrastructurev1alpha1.KopsMachinePool) {
	controllerutil.AddFinalizer(&kcp, getFinalizerName(r.sg.Name))
	if err := r.Update(ctx, &kcp); err != nil {
		r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, "FailedToUpdate", "failed to add finalizer in %s: %s", kcp.Name, err)
	}

	for _, kmp := range kmps {
		if !controllerutil.ContainsFinalizer(&kmp, getFinalizerName(r.sg.Name)) {
			r.addKMPFinalizer(ctx, kmp)
		}
	}
}
