/*
Copyright 2021.

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// ClusterMeshVPCPeeringReadyCondition reports on the successful reconcile of the clustermesh vpcpeering.
	ClusterMeshVPCPeeringReadyCondition clusterv1beta1.ConditionType = "ClusterMeshVPCPeeringReady"

	// ClusterMeshRoutesReadyCondition reports on the successful reconcile of clustermesh routes.
	ClusterMeshRoutesReadyCondition clusterv1beta1.ConditionType = "ClusterMeshRoutesReady"

	// KopsterraformGenerationReadyCondition reports on the successful reconcile of clustermesh security groups.
	ClusterMeshSecurityGroupsReadyCondition clusterv1beta1.ConditionType = "ClusterMeshSecurityGroupsReady"
)

const (
	// ClusterMeshVPCPeeringFailedReason (Severity=Error) indicates that not all clustermesh vpcpeerings are ready.
	ClusterMeshVPCPeeringFailedReason = "ClusterMeshVPCPeeringFailed"

	// ClusterMeshRoutesFailedReason (Severity=Error) indicates that not all clustermesh necessary routes are ready.
	ClusterMeshRoutesFailedReason = "ClusterMeshRoutesFailed"

	// ClusterMeshSecurityGroupFailedReason (Severity=Error) indicates that not all clustermesh securitygroups are ready.
	ClusterMeshSecurityGroupFailedReason = "ClusterMeshSecurityGroupFailed"
)

type ClusterSpec struct {
	VPCID         string   `json:"vpcID,omitempty"`
	Name          string   `json:"name,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
	Region        string   `json:"region,omitempty"`
	CIDR          string   `json:"cidr,omitempty"`
	RouteTableIDs []string `json:"routeTableIDs,omitempty"`
}

// ClusterMeshSpec defines the desired state of ClusterMesh
type ClusterMeshSpec struct {
	// Clusters describes a AWS Kubernetes cluster.
	Clusters []*ClusterSpec `json:"clusterSpec,omitempty"`
}

// ClusterMeshStatus defines the observed state of ClusterMesh
type ClusterMeshStatus struct {
	CrossplanePeeringRef       []*v1.ObjectReference     `json:"crossplanePeeringRef,omitempty"`
	CrossplaneSecurityGroupRef []*v1.ObjectReference     `json:"crossplaneSecurityGroupRef,omitempty"`
	Conditions                 clusterv1beta1.Conditions `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// ClusterMesh is the Schema for the clustermeshes API
type ClusterMesh struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterMeshSpec   `json:"spec,omitempty"`
	Status ClusterMeshStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterMeshList contains a list of ClusterMesh
type ClusterMeshList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterMesh `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterMesh{}, &ClusterMeshList{})
}

// GetConditions returns the set of conditions for this object.
func (cm *ClusterMesh) GetConditions() clusterv1beta1.Conditions {
	return cm.Status.Conditions
}

// SetConditions sets the conditions on this object.
func (cm *ClusterMesh) SetConditions(conditions clusterv1beta1.Conditions) {
	cm.Status.Conditions = conditions
}
