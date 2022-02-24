package crossplane

import (
	"context"
	"fmt"

	crossec2v1beta1 "github.com/crossplane/provider-aws/apis/ec2/v1beta1"
	securitygroupv1alpha1 "github.com/topfreegames/provider-crossplane/apis/securitygroup/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewCrossplaneSecurityGroup(ctx context.Context, name, namespace string, vpcId, region *string, iRules []securitygroupv1alpha1.IngressRule) *crossec2v1beta1.SecurityGroup {
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

func ManageCrossplaneSecurityGroupResource(ctx context.Context, kubeClient client.Client, csg *crossec2v1beta1.SecurityGroup) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info(fmt.Sprintf("creating csg %s", csg.ObjectMeta.GetName()))
	if err := kubeClient.Create(ctx, csg); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		log.Info(fmt.Sprintf("csg %s already exists, updating", csg.ObjectMeta.GetName()))

		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			key := client.ObjectKey{
				Namespace: csg.ObjectMeta.Namespace,
				Name:      csg.ObjectMeta.Name,
			}
			var currentCSG crossec2v1beta1.SecurityGroup
			err := kubeClient.Get(ctx, key, &currentCSG)
			if err != nil {
				return err
			}
			currentCSG.Spec = csg.Spec

			err = kubeClient.Update(ctx, &currentCSG)
			return err
		})
		if retryErr != nil {
			return retryErr
		}
	}
	return nil
}
