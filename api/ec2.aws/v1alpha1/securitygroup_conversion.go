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

package v1alpha1

import (
	"github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	securitygroupv1alpha2 "github.com/topfreegames/provider-crossplane/api/ec2.aws/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

// ConvertTo converts this CronJob to the Hub version (v1).
func (src *SecurityGroup) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*securitygroupv1alpha2.SecurityGroup)

	// ObjectMeta
	dst.ObjectMeta = src.ObjectMeta

	// Spec
	dst.Spec.InfrastructureRef = []*corev1.ObjectReference{
		src.Spec.InfrastructureRef,
	}

	dst.Spec.IngressRules = []securitygroupv1alpha2.IngressRule{}

	for _, ingressRule := range src.Spec.IngressRules {
		dst.Spec.IngressRules = append(dst.Spec.IngressRules, securitygroupv1alpha2.IngressRule{
			IPProtocol:        ingressRule.IPProtocol,
			FromPort:          ingressRule.FromPort,
			ToPort:            ingressRule.ToPort,
			AllowedCIDRBlocks: ingressRule.AllowedCIDRBlocks,
		})
	}

	// Status
	dst.Status.Conditions = src.Status.Conditions
	dst.Status.FailureMessage = src.Status.FailureMessage
	dst.Status.Ready = src.Status.Ready

	return nil
}

func (dst *SecurityGroup) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.SecurityGroup)

	// ObjectMeta
	dst.ObjectMeta = src.ObjectMeta

	// Spec
	dst.Spec.InfrastructureRef = src.Spec.InfrastructureRef[0]

	for _, ingressRule := range src.Spec.IngressRules {
		dst.Spec.IngressRules = append(dst.Spec.IngressRules, IngressRule{
			IPProtocol:        ingressRule.IPProtocol,
			FromPort:          ingressRule.FromPort,
			ToPort:            ingressRule.ToPort,
			AllowedCIDRBlocks: ingressRule.AllowedCIDRBlocks,
		})
	}

	// Status
	dst.Status.Conditions = src.Status.Conditions
	dst.Status.FailureMessage = src.Status.FailureMessage
	dst.Status.Ready = src.Status.Ready

	return nil
}
