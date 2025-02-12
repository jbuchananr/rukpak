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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type BundleConditionType string

const (
	SourceTypeImage = "image"
	SourceTypeGit   = "git"

	TypeUnpacked = "Unpacked"

	ReasonUnpackPending    = "UnpackPending"
	ReasonUnpacking        = "Unpacking"
	ReasonUnpackSuccessful = "UnpackSuccessful"
	ReasonUnpackFailed     = "UnpackFailed"

	PhasePending   = "Pending"
	PhaseUnpacking = "Unpacking"
	PhaseFailing   = "Failing"
	PhaseUnpacked  = "Unpacked"
)

// BundleSpec defines the desired state of Bundle
type BundleSpec struct {
	// ProvisionerClassName sets the name of the provisioner that should reconcile this BundleInstance.
	ProvisionerClassName string `json:"provisionerClassName"`
	// Source defines the configuration for the underlying Bundle content.
	Source BundleSource `json:"source"`
}

type BundleSource struct {
	// Type defines the kind of Bundle content being sourced.
	Type string `json:"type"`
	// Image is the bundle image that backs the content of this bundle.
	Image *ImageSource `json:"image,omitempty"`
	// Git is the git repository that backs the content of this Bundle.
	Git *GitSource `json:"git,omitempty"`
}

type ImageSource struct {
	// Ref contains the reference to a container image containing Bundle contents.
	Ref string `json:"ref"`
}

type GitSource struct {
	// Repository is a URL link to the git repository containing the bundle.
	// Repository is required and the URL should be parsable by a standard git tool.
	Repository string `json:"repository"`
	// Directory refers to the location of the bundle within the git repository.
	// Directory is optional and if not set defaults to ./manifests.
	Directory string `json:"directory,omitempty"`
	// Ref configures the git source to clone a specific branch, tag, or commit
	// from the specified repo. Ref is required, and exactly one field within Ref
	// is required. Setting more than one field or zero fields will result in an
	// error.
	Ref GitRef `json:"ref"`
}

type GitRef struct {
	// Branch refers to the branch to checkout from the repository.
	// The Branch should contain the bundle manifests in the specified directory.
	Branch string `json:"branch,omitempty"`
	// Tag refers to the tag to checkout from the repository.
	// The Tag should contain the bundle manifests in the specified directory.
	Tag string `json:"tag,omitempty"`
	// Commit refers to the commit to checkout from the repository.
	// The Commit should contain the bundle manifests in the specified directory.
	Commit string `json:"commit,omitempty"`
}

type ProvisionerID string

// BundleStatus defines the observed state of Bundle
type BundleStatus struct {
	Info               *BundleInfo        `json:"info,omitempty"`
	Phase              string             `json:"phase,omitempty"`
	Digest             string             `json:"digest,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type BundleInfo struct {
	Package string         `json:"package"`
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Objects []BundleObject `json:"objects,omitempty"`
}

type BundleObject struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name=Image,type=string,JSONPath=`.spec.source.image.ref`
//+kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// Bundle is the Schema for the bundles API
type Bundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BundleSpec   `json:"spec"`
	Status BundleStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BundleList contains a list of Bundle
type BundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bundle `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Bundle{}, &BundleList{})
}
