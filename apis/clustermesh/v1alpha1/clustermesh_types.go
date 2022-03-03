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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterMeshSpec defines the desired state of ClusterMesh
type ClusterMeshSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// ClusterRefList is a reference to a provider-specific resource.
	ClusterRefList []*corev1.ObjectReference `json:"clusterRefList,omitempty"`
}

// ClusterMeshStatus defines the observed state of ClusterMesh
type ClusterMeshStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
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
