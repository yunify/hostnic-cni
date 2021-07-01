/*
Copyright 2017 The Kubernetes Authors.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// VxNetPool is a specification for a VxNetPool resource
type VxNetPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec VxNetPoolSpec `json:"spec"`
	// +optional
	Status VxNetPoolStatus `json:"status"`
}

type VxnetInfo struct {
	Name string `json:"name"`
}

// VxNetPoolSpec is the spec for a VxNetPool resource
type VxNetPoolSpec struct {
	// vxnets in VxNetPool
	Vxnets []VxnetInfo `json:"vxnets"`

	// The block size to use for IP address assignments from this pool. Defaults to 26 for IPv4 and 112 for IPv6.
	BlockSize int `json:"blockSize"`
}

type PoolInfo struct {
	Name    string   `json:"name"`
	IPPool  string   `json:"ippool"`
	Subnets []string `json:"subnets,omitempty"`
}

// VxNetPoolStatus is the status for a VxNetPool resource
type VxNetPoolStatus struct {
	// +optional
	Ready bool `json:"ready"`
	// +optional
	Message *string `json:"message,omitempty"`
	// +optional
	Process *string `json:"process,omitempty"`
	// +optional
	Pools []PoolInfo `json:"pools,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient:nonNamespaced

// VxNetPoolList is a list of VxNetPool resources
type VxNetPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []VxNetPool `json:"items"`
}
