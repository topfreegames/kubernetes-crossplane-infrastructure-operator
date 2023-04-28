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

	oceanaws "github.com/spotinst/spotinst-sdk-go/service/ocean/providers/aws"
	"github.com/topfreegames/provider-crossplane/pkg/spot"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
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
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
	awsConfig          aws.Config
	ec2Client          ec2.EC2Client
	asgClient          autoscaling.AutoScalingClient
	sg                 *securitygroupv1alpha1.SecurityGroup
	kmp                *kinfrastructurev1alpha1.KopsMachinePool
	kcp                *kcontrolplanev1alpha1.KopsControlPlane
	providerConfigName string
}

//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ec2.aws.wildlife.io,resources=securitygroups/finalizers,verbs=update
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=kopsmachinepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=kopscontrolplanes,verbs=get;list;watch
//+kubebuilder:rbac:groups=ec2.aws.crossplane.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete

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

func awsConfigForCredential(ctx context.Context, region string, accessKey string, secretAccessKey string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     accessKey,
				SecretAccessKey: secretAccessKey,
			},
		}),
	)
}

func (c *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {
	r := &SecurityGroupReconciliation{
		SecurityGroupReconciler: *c,
		start:                   time.Now(),
		log:                     ctrl.LoggerFrom(ctx),
	}

	r.sg = &securitygroupv1alpha1.SecurityGroup{}
	if err := r.Get(ctx, req.NamespacedName, r.sg); err != nil {
		return resultError, client.IgnoreNotFound(err)
	}

	if r.sg.Spec.InfrastructureRef == nil {
		return resultDefault, fmt.Errorf("infrastructureRef not supported")
	}

	switch r.sg.Spec.InfrastructureRef.Kind {
	case "KopsMachinePool":
		r.kmp = &kinfrastructurev1alpha1.KopsMachinePool{}
		key := client.ObjectKey{
			Name:      r.sg.Spec.InfrastructureRef.Name,
			Namespace: r.sg.Spec.InfrastructureRef.Namespace,
		}
		if err := r.Client.Get(ctx, key, r.kmp); err != nil {
			return resultError, err
		}

		r.kcp = &kcontrolplanev1alpha1.KopsControlPlane{}
		key = client.ObjectKey{
			Name:      r.kmp.Spec.ClusterName,
			Namespace: r.kmp.ObjectMeta.Namespace,
		}
		if err := r.Client.Get(ctx, key, r.kcp); err != nil {
			return resultError, err
		}
	case "KopsControlPlane":
		r.kcp = &kcontrolplanev1alpha1.KopsControlPlane{}
		key := client.ObjectKey{
			Name:      r.sg.Spec.InfrastructureRef.Name,
			Namespace: r.sg.Spec.InfrastructureRef.Namespace,
		}
		if err := r.Client.Get(ctx, key, r.kcp); err != nil {
			return resultError, err
		}
	default:
		return resultError, fmt.Errorf("infrastructureRef not supported")
	}

	subnet, err := kops.GetSubnetFromKopsControlPlane(r.kcp)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("failed to get subnet from kcp"))
		return resultError, err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("failed to get region from kcp"))
		return resultError, err
	}

	var secretName, namespace string
	if r.kcp.Spec.IdentityRef != nil {
		secretName = r.kcp.Spec.IdentityRef.Name
		// gambiarra??
		if r.kcp.Spec.IdentityRef.Namespace == "" {
			namespace = "kubernetes-kops-operator-system"
		} else {
			namespace = r.kcp.Spec.IdentityRef.Namespace
		}
	} else {
		secretName = "default"
		namespace = "kubernetes-kops-operator-system"
	}

	r.providerConfigName = secretName

	awsCreds := &corev1.Secret{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}
	if err := c.Get(ctx, key, awsCreds); err != nil {
		r.log.Error(err, fmt.Sprintf("failed to get secret"))
		return resultError, err
	}

	accessKeyBytes, ok := awsCreds.Data["AccessKeyID"]
	if !ok {

	}
	accessKey := string(accessKeyBytes)

	secretAccessKeyBytes, ok := awsCreds.Data["SecretAccessKey"]
	if !ok {

	}
	secretAccessKey := string(secretAccessKeyBytes)

	cfg, err := awsConfigForCredential(ctx, *region, accessKey, secretAccessKey)

	r.ec2Client = r.NewEC2ClientFactory(cfg)
	r.asgClient = r.NewAutoScalingClientFactory(cfg)

	r.log.Info(fmt.Sprintf("starting reconcile loop for %s", r.sg.ObjectMeta.GetName()))

	// Initialize the patch helper.
	patchHelper, err := patch.NewHelper(r.sg, r.Client)
	if err != nil {
		r.log.Error(err, "failed to initialize patch helper")
		return resultError, err
	}

	defer func() {
		err = patchHelper.Patch(ctx, r.sg)
		if err != nil {
			r.log.Error(rerr, "Failed to patch security group", "sgName", r.sg.Name)
			if rerr == nil {
				rerr = err
			}
		}
		r.log.Info(fmt.Sprintf("finished reconcile loop for %s", r.sg.ObjectMeta.GetName()))
	}()

	return r.Reconcile(ctx)
}

func (r *SecurityGroupReconciliation) Reconcile(ctx context.Context) (ctrl.Result, error) {
	if !r.sg.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, r.sg)
	}

	if !controllerutil.ContainsFinalizer(r.sg, securityGroupFinalizer) {
		controllerutil.AddFinalizer(r.sg, securityGroupFinalizer)
	}

	result, err := r.reconcileNormal(ctx)
	durationMsg := fmt.Sprintf("finished reconcile security groups loop for %s finished in %s ", r.sg.ObjectMeta.Name, time.Since(r.start).String())
	if result.RequeueAfter > 0 {
		durationMsg = fmt.Sprintf("%s, next run in %s", durationMsg, result.RequeueAfter.String())
	}
	r.log.Info(durationMsg)
	return result, err
}

func (r *SecurityGroupReconciliation) reconcileDelete(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup) (ctrl.Result, error) {
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

	err = r.deleteSGFromASG(ctx, sg, csg)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Delete(ctx, csg)
	if err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(sg, securityGroupFinalizer)
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciliation) reconcileNormal(ctx context.Context) (ctrl.Result, error) {

	if r.kmp != nil {
		err := r.reconcileKopsMachinePool(ctx)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return requeue30seconds, nil
			}
			return resultError, err
		}
	} else {
		err := r.reconcileKopsControlPlane(ctx)
		if err != nil {
			if errors.Is(err, ErrSecurityGroupNotAvailable) {
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			return resultError, err
		}
	}

	r.sg.Status.Ready = true
	return resultDefault, nil
}

// TODO: Improve test coverage of this method
func (r *SecurityGroupReconciliation) reconcileKopsControlPlane(ctx context.Context) error {
	vpcId, region, err := r.getAwsAccountInfo(ctx, r.kcp)
	if err != nil {
		return err
	}

	csg, err := crossplane.CreateOrUpdateCrossplaneSecurityGroup(ctx, r.Client, vpcId, region, r.providerConfigName, r.sg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return fmt.Errorf("error reconciling crossplane securitygroup: %w", err)
	}

	if r.sg.GetAnnotations()[AnnotationKeyReconciliationPaused] == "true" {
		r.log.Info("Reconciliation is paused via the pause annotation", "annotation", AnnotationKeyReconciliationPaused, "value", "true")
		r.Recorder.Eventf(r.sg, corev1.EventTypeNormal, securitygroupv1alpha1.ReasonReconcilePaused, "Reconciliation is paused via the pause annotation")
		return nil
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return err
	}
	conditions.MarkTrue(r.sg, securitygroupv1alpha1.CrossplaneResourceReadyCondition)

	availableCondition := crossplane.GetSecurityGroupReadyCondition(csg)
	if availableCondition == nil {
		return ErrSecurityGroupNotAvailable
	} else {
		if availableCondition.Status == corev1.ConditionTrue {
			conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupReadyCondition)
		} else {
			conditions.MarkFalse(r.sg,
				securitygroupv1alpha1.SecurityGroupReadyCondition,
				string(availableCondition.Reason),
				clusterv1beta1.ConditionSeverityError,
				availableCondition.Message,
			)
			return ErrSecurityGroupNotAvailable
		}
	}

	kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", r.kcp.Name)
	if err != nil {
		return err
	}

	var attachErr error
	for _, kmp := range kmps {
		if len(kmp.Spec.SpotInstOptions) != 0 {
			oceanClient := r.NewOceanCloudProviderAWSFactory()
			launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, kmp.Spec.ClusterName)
			if err != nil {
				return fmt.Errorf("error retrieving vngs from clusterName: %w", err)
			}
			err = r.attachSGToVNG(ctx, oceanClient, launchSpecs, kmp.Name, csg)
			if err != nil {
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha1.SecurityGroupAttachmentFailedReason, err.Error())
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
				r.Recorder.Eventf(r.sg, corev1.EventTypeWarning, securitygroupv1alpha1.SecurityGroupAttachmentFailedReason, err.Error())
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		}
	}

	if attachErr != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha1.SecurityGroupAttachedCondition,
			securitygroupv1alpha1.SecurityGroupAttachmentFailedReason,
			clusterv1beta1.ConditionSeverityError,
			attachErr.Error(),
		)
	} else {
		conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupAttachedCondition)
		conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupReadyCondition)
	}

	return attachErr
}

func (r *SecurityGroupReconciliation) reconcileKopsMachinePool(ctx context.Context) error {
	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key := client.ObjectKey{
		Namespace: r.sg.Spec.InfrastructureRef.Namespace,
		Name:      r.kmp.Spec.ClusterName,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return err
	}

	vpcId, region, err := r.getAwsAccountInfo(ctx, kcp)
	if err != nil {
		return fmt.Errorf("error retrieving aws account: %w", err)
	}

	csg, err := crossplane.CreateOrUpdateCrossplaneSecurityGroup(ctx, r.Client, vpcId, region, r.providerConfigName, r.sg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return fmt.Errorf("error creating crossplane securitygroup: %w", err)
	}

	if r.sg.GetAnnotations()[AnnotationKeyReconciliationPaused] == "true" {
		r.log.Info("Reconciliation is paused via the pause annotation", "annotation", AnnotationKeyReconciliationPaused, "value", "true")
		r.Recorder.Eventf(r.sg, corev1.EventTypeNormal, securitygroupv1alpha1.ReasonReconcilePaused, "Reconciliation is paused via the pause annotation")
		return nil
	}

	err = r.Client.Get(ctx, client.ObjectKeyFromObject(csg), csg)
	if err != nil {
		conditions.MarkFalse(r.sg,
			securitygroupv1alpha1.CrossplaneResourceReadyCondition,
			securitygroupv1alpha1.CrossplaneResourceReconciliationFailedReason,
			clusterv1beta1.ConditionSeverityError,
			err.Error(),
		)
		return err
	}
	conditions.MarkTrue(r.sg, securitygroupv1alpha1.CrossplaneResourceReadyCondition)

	availableCondition := crossplane.GetSecurityGroupReadyCondition(csg)
	if availableCondition == nil {
		return ErrSecurityGroupNotAvailable
	} else {
		if availableCondition.Status == corev1.ConditionTrue {
			conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupReadyCondition)
		} else {
			conditions.MarkFalse(r.sg,
				securitygroupv1alpha1.SecurityGroupReadyCondition,
				string(availableCondition.Reason),
				clusterv1beta1.ConditionSeverityError,
				availableCondition.Message,
			)
			return ErrSecurityGroupNotAvailable
		}
	}

	if len(r.kmp.Spec.SpotInstOptions) != 0 {
		oceanClient := r.NewOceanCloudProviderAWSFactory()
		launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, r.kmp.Spec.ClusterName)
		if err != nil {
			return fmt.Errorf("error retrieving vngs from clusterName: %w", err)
		}
		err = r.attachSGToVNG(ctx, oceanClient, launchSpecs, r.kmp.Name, csg)
		if err != nil {
			conditions.MarkFalse(r.sg,
				securitygroupv1alpha1.SecurityGroupAttachedCondition,
				securitygroupv1alpha1.SecurityGroupAttachmentFailedReason,
				clusterv1beta1.ConditionSeverityError,
				err.Error(),
			)
			return err
		}

	} else {
		asgName, err := kops.GetCloudResourceNameFromKopsMachinePool(*r.kmp)
		if err != nil {
			return fmt.Errorf("error retrieving ASG name: %w", err)
		}

		err = r.attachSGToASG(ctx, asgName, csg.Status.AtProvider.SecurityGroupID)
		if err != nil {
			conditions.MarkFalse(r.sg,
				securitygroupv1alpha1.SecurityGroupAttachedCondition,
				securitygroupv1alpha1.SecurityGroupAttachmentFailedReason,
				clusterv1beta1.ConditionSeverityError,
				err.Error(),
			)
			return err
		}

	}

	conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupAttachedCondition)
	conditions.MarkTrue(r.sg, securitygroupv1alpha1.SecurityGroupReadyCondition)

	return nil
}

func (r *SecurityGroupReconciliation) getAwsAccountInfo(ctx context.Context, kcp *kcontrolplanev1alpha1.KopsControlPlane) (*string, *string, error) {
	subnet, err := kops.GetSubnetFromKopsControlPlane(kcp)
	if err != nil {
		return aws.String(""), aws.String(""), err
	}

	region, err := kops.GetRegionFromKopsSubnet(*subnet)
	if err != nil {
		return aws.String(""), aws.String(""), err
	}

	if region == nil {
		return aws.String(""), aws.String(""), fmt.Errorf("region not defined")
	}

	if err != nil {
		return aws.String(""), aws.String(""), err
	}

	vpcId, err := ec2.GetVPCIdFromCIDR(ctx, r.ec2Client, kcp.Spec.KopsClusterSpec.NetworkCIDR)
	if err != nil {
		return aws.String(""), aws.String(""), err
	}

	if vpcId == nil {
		return aws.String(""), aws.String(""), fmt.Errorf("vpcId not defined")
	}

	return vpcId, region, nil
}

func (r *SecurityGroupReconciliation) attachSGToVNG(ctx context.Context, oceanClient spot.OceanClient, launchSpecs []*oceanaws.LaunchSpec, kmpName string, csg *crossec2v1beta1.SecurityGroup) error {
	for _, vng := range launchSpecs {
		for _, labels := range vng.Labels {
			if *labels.Key == "kops.k8s.io/instance-group-name" && *labels.Value == kmpName {
				securityGroupsIDs := vng.SecurityGroupIDs
				if slices.Contains(securityGroupsIDs, csg.Status.AtProvider.SecurityGroupID) {
					return nil
				}
				securityGroupsIDs = append(securityGroupsIDs, csg.Status.AtProvider.SecurityGroupID)
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

func (r *SecurityGroupReconciliation) attachSGToASG(ctx context.Context, asgName, sgId string) error {

	asg, err := autoscaling.GetAutoScalingGroupByName(ctx, r.asgClient, asgName)
	if err != nil {
		return err
	}

	launchTemplateVersion, err := ec2.GetLastLaunchTemplateVersion(ctx, r.ec2Client, *asg.LaunchTemplate.LaunchTemplateId)
	if err != nil {
		return err
	}

	ltVersionOutput, err := ec2.AttachSecurityGroupToLaunchTemplate(ctx, r.ec2Client, sgId, launchTemplateVersion)
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

// Remove the security group from ASG the removing it from the launch template
func (r *SecurityGroupReconciliation) deleteSGFromASG(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup, csg *crossec2v1beta1.SecurityGroup) error {

	switch sg.Spec.InfrastructureRef.Kind {
	case "KopsControlPlane":
		kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
		key := client.ObjectKey{
			Namespace: sg.Spec.InfrastructureRef.Namespace,
			Name:      sg.Spec.InfrastructureRef.Name,
		}
		if err := r.Client.Get(ctx, key, kcp); err != nil {
			return err
		}

		err := r.deleteSGFromKopsControlPlaneASGs(ctx, sg, csg, kcp)
		if err != nil {
			return err
		}
	case "KopsMachinePool":
		kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
		key := client.ObjectKey{
			Name:      sg.Spec.InfrastructureRef.Name,
			Namespace: sg.Spec.InfrastructureRef.Namespace,
		}
		if err := r.Client.Get(ctx, key, kmp); err != nil {
			return err
		}

		err := r.deleteSGFromKopsMachinePoolASG(ctx, sg, csg, kmp)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("infrastructureRef not supported")
	}

	return nil
}

func (r *SecurityGroupReconciliation) deleteSGFromKopsControlPlaneASGs(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup, csg *crossec2v1beta1.SecurityGroup, kcp *kcontrolplanev1alpha1.KopsControlPlane) error {
	kmps, err := kops.GetKopsMachinePoolsWithLabel(ctx, r.Client, "cluster.x-k8s.io/cluster-name", kcp.Name)
	if err != nil {
		return err
	}

	var attachErr error
	for _, kmp := range kmps {
		if len(kmp.Spec.SpotInstOptions) != 0 {
			oceanClient := r.NewOceanCloudProviderAWSFactory()
			launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, kmp.Spec.ClusterName)
			if err != nil {
				return fmt.Errorf("error retrieving vngs from clusterName: %w", err)
			}
			err = r.detachSGFromVNG(ctx, oceanClient, launchSpecs, kmp.Name, csg)
			if err != nil {
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		} else {
			asgName, err := kops.GetCloudResourceNameFromKopsMachinePool(kmp)
			if err != nil {
				attachErr = multierror.Append(attachErr, err)
				continue
			}

			err = r.detachSGFromASG(ctx, asgName, csg.Status.AtProvider.SecurityGroupID)
			if err != nil {
				attachErr = multierror.Append(attachErr, err)
				continue
			}
		}
	}

	if attachErr != nil {
		return attachErr
	}

	return nil
}

func (r *SecurityGroupReconciliation) deleteSGFromKopsMachinePoolASG(ctx context.Context, sg *securitygroupv1alpha1.SecurityGroup, csg *crossec2v1beta1.SecurityGroup, kmp *kinfrastructurev1alpha1.KopsMachinePool) error {
	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key := client.ObjectKey{
		Namespace: sg.Spec.InfrastructureRef.Namespace,
		Name:      kmp.Spec.ClusterName,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return err
	}

	if len(kmp.Spec.SpotInstOptions) != 0 {
		oceanClient := r.NewOceanCloudProviderAWSFactory()
		launchSpecs, err := spot.ListVNGsFromClusterName(ctx, oceanClient, kmp.Spec.ClusterName)
		if err != nil {
			return fmt.Errorf("error retrieving vngs from clusterName: %w", err)
		}

		return r.detachSGFromVNG(ctx, oceanClient, launchSpecs, kmp.Name, csg)

	} else {
		asgName, err := kops.GetCloudResourceNameFromKopsMachinePool(*kmp)
		if err != nil {
			return err
		}

		return r.detachSGFromASG(ctx, asgName, csg.Status.AtProvider.SecurityGroupID)
	}
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
		For(&securitygroupv1alpha1.SecurityGroup{}).
		Complete(r)
}
