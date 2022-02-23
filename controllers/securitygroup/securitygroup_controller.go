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
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	"github.com/go-logr/logr"
	kcontrolplanev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/controlplane/v1alpha1"
	kinfrastructurev1alpha1 "github.com/topfreegames/kubernetes-kops-operator/apis/infrastructure/v1alpha1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	kopsapi "k8s.io/kops/pkg/apis/kops"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	log                     logr.Logger
	GetVPCIdFromCIDRFactory func(string, string) (*string, error)
}

func GetVPCIdFromCIDR(region, CIDR string) (*string, error) {
	awsClient, err := session.NewSession(
		&aws.Config{
			Region: aws.String(region),
		},
	)
	if err != nil {
		return nil, err
	}
	ec2svc := ec2.New(awsClient)
	filter := "cidr"
	result, err := ec2svc.DescribeVpcs(&ec2.DescribeVpcsInput{
		Filters: []*ec2.Filter{
			{
				Name: &filter,
				Values: []*string{
					&CIDR,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	if len(result.Vpcs) == 0 {
		return nil, fmt.Errorf("failed to retrieve vpc with CIDR: %v", result)
	}

	return result.Vpcs[0].VpcId, nil
}

func (r *SecurityGroupReconciler) getRegionFromKopsSubnet(subnet kopsapi.ClusterSubnetSpec) (*string, error) {
	if subnet.Region != "" {
		return &subnet.Region, nil
	}

	if subnet.Zone != "" {
		zone := subnet.Zone
		region := zone[:len(zone)-1]
		return &region, nil
	}

	return nil, fmt.Errorf("couldn't get region from KopsControlPlane")
}

func (r *SecurityGroupReconciler) newCrossplaneSecurityGroup(ctx context.Context, name, namespace string, vpcId, region *string, iRules []securitygroupv1alpha1.IngressRule) *crossec2v1beta1.SecurityGroup {
	csg := &crossec2v1beta1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: crossec2v1beta1.SecurityGroupSpec{
			ForProvider: crossec2v1beta1.SecurityGroupParameters{
				Description: fmt.Sprintf("sg %s managed by provider-crossplane", name),
				GroupName:   name,
				Ingress:     []crossec2v1beta1.IPPermission{},
				VPCID:       vpcId,
				Region:      region,
			},
		},
	}

	var ingressRules []crossec2v1beta1.IPPermission
	for _, ingressRule := range iRules {
		ipPermission := crossec2v1beta1.IPPermission{
			FromPort:   &ingressRule.FromPort,
			ToPort:     &ingressRule.ToPort,
			IPProtocol: ingressRule.IPProtocol,
		}
		var allowedCIDRBlocks []crossec2v1beta1.IPRange
		for _, allowedCIDR := range ingressRule.AllowedCIDRBlocks {
			ipRange := crossec2v1beta1.IPRange{
				CIDRIP: allowedCIDR,
			}
			allowedCIDRBlocks = append(allowedCIDRBlocks, ipRange)
		}
		ipPermission.IPRanges = allowedCIDRBlocks
		ingressRules = append(ingressRules, ipPermission)
	}
	csg.Spec.ForProvider.Ingress = ingressRules

	return csg
}

func (r *SecurityGroupReconciler) retrieveKopsMachinePoolInfo(ctx context.Context, sg securitygroupv1alpha1.SecurityGroup) (*string, *string, error) {
	// Fetch KopsMachinePool
	kmp := &kinfrastructurev1alpha1.KopsMachinePool{}
	key := client.ObjectKey{Name: sg.Spec.InfrastructureRef.Name, Namespace: sg.Spec.InfrastructureRef.Namespace}
	if err := r.Client.Get(ctx, key, kmp); err != nil {
		return nil, nil, err
	}

	// Fetch Cluster
	cluster := &clusterv1beta1.Cluster{}
	key = client.ObjectKey{
		Namespace: kmp.ObjectMeta.Namespace,
		Name:      kmp.Spec.ClusterName,
	}
	if err := r.Client.Get(ctx, key, cluster); err != nil {
		return nil, nil, err
	}

	// Fetch KopsControlPlane
	kcp := &kcontrolplanev1alpha1.KopsControlPlane{}
	key = client.ObjectKey{
		Namespace: cluster.Spec.ControlPlaneRef.Namespace,
		Name:      cluster.Spec.ControlPlaneRef.Name,
	}
	if err := r.Client.Get(ctx, key, kcp); err != nil {
		return nil, nil, err
	}

	// Fetch subnet
	if kcp.Spec.KopsClusterSpec.Subnets == nil {
		return nil, nil, fmt.Errorf("subnet not found in KopsControlPlane")
	}
	subnet := kcp.Spec.KopsClusterSpec.Subnets[0]

	// Fetch region
	region, err := r.getRegionFromKopsSubnet(subnet)
	if err != nil {
		return nil, nil, err
	}

	// Fetch vpcId
	vpcId, err := r.GetVPCIdFromCIDRFactory(*region, kcp.Spec.KopsClusterSpec.NetworkCIDR)
	if err != nil {
		return nil, nil, err
	}

	return region, vpcId, nil
}

func (r *SecurityGroupReconciler) manageCrossplaneSecurityGroupResource(ctx context.Context, csg *crossec2v1beta1.SecurityGroup) error {
	r.log.Info(fmt.Sprintf("creating csg %s", csg.ObjectMeta.GetName()))
	if err := r.Client.Create(ctx, csg); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		r.log.Info(fmt.Sprintf("csg %s already exists, updating", csg.ObjectMeta.GetName()))

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			key := client.ObjectKey{
				Namespace: csg.ObjectMeta.Namespace,
				Name:      csg.ObjectMeta.Name,
			}
			var currentCSG crossec2v1beta1.SecurityGroup
			err := r.Get(ctx, key, &currentCSG)
			if err != nil {
				return err
			}
			currentCSG.Spec = csg.Spec

			err = r.Client.Update(ctx, &currentCSG)
			return err
		})
		if retryErr != nil {
			return retryErr
		}
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

	var region *string
	var vpcId *string

	// Only supports KopsMachinePool for now
	switch securityGroup.Spec.InfrastructureRef.Kind {
	case "KopsMachinePool":
		var err error
		region, vpcId, err = r.retrieveKopsMachinePoolInfo(ctx, *securityGroup)
		if err != nil {
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, fmt.Errorf("infrastructureRef not supported")
	}

	if vpcId == nil {
		return ctrl.Result{}, fmt.Errorf("vpcId not defined")
	}

	if region == nil {
		return ctrl.Result{}, fmt.Errorf("region not defined")
	}

	csg := r.newCrossplaneSecurityGroup(ctx, securityGroup.ObjectMeta.Name, securityGroup.ObjectMeta.Namespace, vpcId, region, securityGroup.Spec.IngressRules)
	err := r.manageCrossplaneSecurityGroupResource(ctx, csg)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&securitygroupv1alpha1.SecurityGroup{}).
		Complete(r)
}
