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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IngressRule struct {
	// The IP protocol name (tcp, udp, icmp) or number (see Protocol Numbers
	// (http://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml)).
	Protocol string `json:"protocol,omitempty"`
	// The start of port range for the TCP and UDP protocols, or an ICMP code.
	// A value of -1 indicates all ICMP codes.
	FromPort int `json:"fromPort,omitempty"`
	// The end of port range for the TCP and UDP protocols, or an ICMP code.
	// A value of -1 indicates all ICMP codes.
	ToPort int `json:"toPort,omitempty"`
	// AllowedCIDRBlocks is a list of CIDR blocks allowed to access the referenced infrastructure.
	AllowedCIDRBlocks []string `json:"allowedCIDRs,omitempty"`
}

// SecurityGroupSpec defines the desired state of SecurityGroup
type SecurityGroupSpec struct {
	IngressRules []IngressRule `json:"ingressRules,omitempty"`
	// InfrastructureRef is a reference to a provider-specific resource.
	InfrastructureRef *corev1.ObjectReference `json:"infrastructureRef,omitempty"`
}

// SecurityGroupStatus defines the observed state of SecurityGroup
type SecurityGroupStatus struct{}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SecurityGroup is the Schema for the securitygroups API
type SecurityGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecurityGroupSpec   `json:"spec,omitempty"`
	Status SecurityGroupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SecurityGroupList contains a list of SecurityGroup
type SecurityGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecurityGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SecurityGroup{}, &SecurityGroupList{})
}
