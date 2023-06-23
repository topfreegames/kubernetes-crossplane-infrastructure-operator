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
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// CrossplaneResourceReadyCondition reports on the successful management of the Crossplane resource.
	CrossplaneResourceReadyCondition clusterv1beta1.ConditionType = "CrossplaneResourceReady"

	// SecurityGroupReadyCondition reports on the readiness of the SecurityGroup in AWS.
	SecurityGroupReadyCondition clusterv1beta1.ConditionType = "SecurityGroupReady"

	// SecurityGroupAttachedCondition reports on the successful attachment of the SecurityGroup in the InfrastructureRef.
	SecurityGroupAttachedCondition clusterv1beta1.ConditionType = "SecurityGroupAttachedReady"
)

const (
	// CrossplaneResourceReconciliationFailedReason (Severity=Error) indicates that Crossplane resource couldn't be created/updated.
	CrossplaneResourceReconciliationFailedReason = "CrossplaneResourceReconciliationFailed"

	// SecurityGroupAttachmentFailedReason (Severity=Error) indicates that the SecurityGroup couldn´t be attached in the InfrastructureRef.
	SecurityGroupAttachmentFailedReason = "SecurityGroupAttachmentReconciliationFailed"

	// SecurityGroupAttachmentFailedReason (Severity=Normal) indicates that the reconciliation for the SecurityGroup is paused.
	ReasonReconcilePaused = "ReconcilePaused"
)

type IngressRule struct {
	// The IP protocol name (tcp, udp, icmp) or number (see Protocol Numbers
	// (http://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml)).
	IPProtocol string `json:"ipProtocol,omitempty"`
	// The start of port range for the TCP and UDP protocols, or an ICMP code.
	// A value of -1 indicates all ICMP codes.
	FromPort int32 `json:"fromPort,omitempty"`
	// The end of port range for the TCP and UDP protocols, or an ICMP code.
	// A value of -1 indicates all ICMP codes.
	ToPort int32 `json:"toPort,omitempty"`
	// AllowedCIDRBlocks is a list of CIDR blocks allowed to access the referenced infrastructure.
	AllowedCIDRBlocks []string `json:"allowedCIDRs,omitempty"`
}

// SecurityGroupSpec defines the desired state of SecurityGroup
type SecurityGroupSpec struct {

	// IngressRules is a list of ingress rules to apply to the Crossplane SecurityGroup.
	IngressRules []IngressRule `json:"ingressRules,omitempty"`
	// InfrastructureRef is a reference to a provider-specific resource.
	InfrastructureRef []*corev1.ObjectReference `json:"infrastructureRef,omitempty"`
}

// SecurityGroupStatus defines the observed state of SecurityGroup
type SecurityGroupStatus struct {
	// Ready denotes that the SecurityGroup resource is created and attached
	// +kubebuilder:default=false
	Ready bool `json:"ready,omitempty"`

	// ErrorMessage indicates that there is a terminal problem reconciling the
	// state, and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the SecurityGroup.
	// +optional
	Conditions clusterv1beta1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:shortName=wsg
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""
//+kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""

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

// GetConditions returns the set of conditions for this object.
func (sg *SecurityGroup) GetConditions() clusterv1beta1.Conditions {
	return sg.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (sg *SecurityGroup) SetConditions(conditions clusterv1beta1.Conditions) {
	sg.Status.Conditions = conditions
}
