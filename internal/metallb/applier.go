/*
Copyright 2025.

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

package metallb

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// ApplyYAML applies YAML manifest to the cluster
func ApplyYAML(ctx context.Context, dynamicClient dynamic.Interface, yamlContent string) error {
	// Parse YAML to unstructured object
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(yamlContent), obj); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Get GVR from the object
	gvk := obj.GroupVersionKind()
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: resourceForKind(gvk.Kind),
	}

	namespace := obj.GetNamespace()

	// Get resource interface
	var resourceInterface dynamic.ResourceInterface
	if namespace != "" {
		resourceInterface = dynamicClient.Resource(gvr).Namespace(namespace)
	} else {
		resourceInterface = dynamicClient.Resource(gvr)
	}

	// Try to get existing resource
	existing, err := resourceInterface.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create new resource
			_, err = resourceInterface.Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create resource: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Update existing resource
	obj.SetResourceVersion(existing.GetResourceVersion())
	_, err = resourceInterface.Update(ctx, obj, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	return nil
}

// resourceForKind returns the resource name for a given kind
func resourceForKind(kind string) string {
	// Simple pluralization - in production, use a proper pluralizer
	switch kind {
	case "Service":
		return "services"
	case "IPAddressPool":
		return "ipaddresspools"
	case "L2Advertisement":
		return "l2advertisements"
	case "Namespace":
		return "namespaces"
	default:
		return kind + "s"
	}
}

// EnsureNamespace ensures a namespace exists
func EnsureNamespace(ctx context.Context, clientset *kubernetes.Clientset, name string) error {
	_, err := clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Create namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
				},
			}
			_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create namespace: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get namespace: %w", err)
	}
	return nil
}

// GetServiceLoadBalancerIP gets the LoadBalancer IP from a Service
func GetServiceLoadBalancerIP(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) (string, error) {
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service: %w", err)
	}

	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return "", fmt.Errorf("service has no LoadBalancer ingress")
	}

	ip := svc.Status.LoadBalancer.Ingress[0].IP
	if ip == "" {
		return "", fmt.Errorf("service LoadBalancer IP is empty")
	}

	return ip, nil
}
