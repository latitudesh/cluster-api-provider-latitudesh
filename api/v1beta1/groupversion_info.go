package v1beta1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

const (
	Group = "infrastructure.cluster.x-k8s.io"
)

// +kubebuilder:object:generate=true
// +groupName=infrastructure.cluster.x-k8s.io

var (
	GroupVersion = schema.GroupVersion{Group: Group, Version: "v1beta1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme = SchemeBuilder.AddToScheme
)

