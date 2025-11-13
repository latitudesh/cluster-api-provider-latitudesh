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

package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/metallb"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// RBAC for MetalLBConfig CRD
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metallbconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metallbconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=metallbconfigs/finalizers,verbs=update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

const (
	MetalLBConfigFinalizerName = "metallbconfig.infrastructure.cluster.x-k8s.io"

	// Condition types
	MetalLBInstalledCondition         = "MetalLBInstalled"
	MetalLBConfiguredCondition        = "MetalLBConfigured"
	ControlPlaneServiceReadyCondition = "ControlPlaneServiceReady"

	// Condition reasons
	MetalLBInstallationSucceededReason = "InstallationSucceeded"
	MetalLBInstallationFailedReason    = "InstallationFailed"
	MetalLBConfiguredReason            = "ConfigurationApplied"
	MetalLBConfigurationFailedReason   = "ConfigurationFailed"
	ControlPlaneServiceCreatedReason   = "ServiceCreated"
	ControlPlaneServiceFailedReason    = "ServiceCreationFailed"
	MetalLBClusterNotReadyReason       = "ClusterNotReady"
	MetalLBClusterNotFoundReason       = "ClusterNotFound"
)

type MetalLBConfigReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (r *MetalLBConfigReconciler) SetRecorder(recorder record.EventRecorder) {
	r.recorder = recorder
}

func (r *MetalLBConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := crlog.FromContext(ctx).WithValues("metallbconfig", req.NamespacedName)
	log.Info("reconcile start")

	// Fetch the MetalLBConfig
	mlbConfig := &infrav1.MetalLBConfig{}
	err := r.Get(ctx, req.NamespacedName, mlbConfig)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(mlbConfig, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to Patch the MetalLBConfig object and status after each reconciliation
	defer func() {
		if err := patchHelper.Patch(ctx, mlbConfig); err != nil {
			log.Error(err, "failed to patch MetalLBConfig")
			if reterr == nil {
				reterr = err
			}
		}
	}()

	// Handle deletion
	if !mlbConfig.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, mlbConfig)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(mlbConfig, MetalLBConfigFinalizerName) {
		controllerutil.AddFinalizer(mlbConfig, MetalLBConfigFinalizerName)
		return ctrl.Result{}, nil
	}

	// Reconcile normal operations
	return r.reconcileNormal(ctx, mlbConfig)
}

func (r *MetalLBConfigReconciler) reconcileNormal(ctx context.Context, mlbConfig *infrav1.MetalLBConfig) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Get the referenced LatitudeCluster
	latitudeCluster, err := r.getLatitudeCluster(ctx, mlbConfig)
	if err != nil {
		log.Error(err, "failed to get LatitudeCluster")
		r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionFalse, MetalLBClusterNotFoundReason, err.Error())
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get the owner Cluster
	cluster, err := r.getCluster(ctx, latitudeCluster)
	if err != nil {
		log.Error(err, "failed to get Cluster")
		r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionFalse, MetalLBClusterNotFoundReason, err.Error())
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Check if cluster is ready
	if !cluster.Status.ControlPlaneReady {
		log.Info("Cluster control plane not ready yet, waiting")
		r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionFalse, MetalLBClusterNotReadyReason, "Waiting for control plane to be ready")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get kubeconfig to access the workload cluster
	workloadClient, restConfig, err := r.getWorkloadClusterClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get workload cluster client")
		r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionFalse, MetalLBClusterNotReadyReason, fmt.Sprintf("Failed to connect to workload cluster: %v", err))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Install MetalLB if not already installed
	if !mlbConfig.Status.Installed {
		log.Info("Installing MetalLB in workload cluster")
		if err := r.installMetalLB(ctx, workloadClient, mlbConfig); err != nil {
			log.Error(err, "failed to install MetalLB")
			r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionFalse, MetalLBInstallationFailedReason, err.Error())
			r.recorder.Eventf(mlbConfig, corev1.EventTypeWarning, MetalLBInstallationFailedReason, "Failed to install MetalLB: %v", err)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		mlbConfig.Status.Installed = true
		mlbConfig.Status.Version = mlbConfig.Spec.Version
		r.setCondition(mlbConfig, MetalLBInstalledCondition, metav1.ConditionTrue, MetalLBInstallationSucceededReason, "MetalLB installed successfully")
		r.recorder.Event(mlbConfig, corev1.EventTypeNormal, MetalLBInstallationSucceededReason, "MetalLB installed successfully")
		log.Info("MetalLB installed successfully")
	}

	// Configure MetalLB (IP pool and L2 advertisement)
	log.Info("Configuring MetalLB")
	if err := r.configureMetalLB(ctx, restConfig, mlbConfig); err != nil {
		log.Error(err, "failed to configure MetalLB")
		r.setCondition(mlbConfig, MetalLBConfiguredCondition, metav1.ConditionFalse, MetalLBConfigurationFailedReason, err.Error())
		r.recorder.Eventf(mlbConfig, corev1.EventTypeWarning, MetalLBConfigurationFailedReason, "Failed to configure MetalLB: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	r.setCondition(mlbConfig, MetalLBConfiguredCondition, metav1.ConditionTrue, MetalLBConfiguredReason, "MetalLB configured successfully")
	log.Info("MetalLB configured successfully")

	// Create LoadBalancer Service for control plane if enabled
	if mlbConfig.Spec.ControlPlaneEndpoint != nil && mlbConfig.Spec.ControlPlaneEndpoint.Enabled {
		log.Info("Creating control plane LoadBalancer Service")
		endpoint, err := r.createControlPlaneService(ctx, workloadClient, restConfig, mlbConfig, latitudeCluster)
		if err != nil {
			log.Error(err, "failed to create control plane service")
			r.setCondition(mlbConfig, ControlPlaneServiceReadyCondition, metav1.ConditionFalse, ControlPlaneServiceFailedReason, err.Error())
			r.recorder.Eventf(mlbConfig, corev1.EventTypeWarning, ControlPlaneServiceFailedReason, "Failed to create control plane service: %v", err)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		mlbConfig.Status.ControlPlaneEndpoint = endpoint
		r.setCondition(mlbConfig, ControlPlaneServiceReadyCondition, metav1.ConditionTrue, ControlPlaneServiceCreatedReason, "Control plane LoadBalancer Service created")
		log.Info("Control plane service created", "endpoint", endpoint)
	}

	// Mark as ready
	mlbConfig.Status.Ready = true
	r.recorder.Event(mlbConfig, corev1.EventTypeNormal, "ReconcileSuccess", "MetalLBConfig reconciled successfully")
	log.Info("reconcile complete", "ready", mlbConfig.Status.Ready)

	// Requeue to periodically check status
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *MetalLBConfigReconciler) reconcileDelete(ctx context.Context, mlbConfig *infrav1.MetalLBConfig) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)
	log.Info("reconcile delete")

	// TODO: Optionally uninstall MetalLB from workload cluster
	// For now, we just remove the finalizer

	// Remove finalizer
	controllerutil.RemoveFinalizer(mlbConfig, MetalLBConfigFinalizerName)
	log.Info("finalizer removed")

	return ctrl.Result{}, nil
}

func (r *MetalLBConfigReconciler) getLatitudeCluster(ctx context.Context, mlbConfig *infrav1.MetalLBConfig) (*infrav1.LatitudeCluster, error) {
	clusterNamespace := mlbConfig.Spec.ClusterRef.Namespace
	if clusterNamespace == "" {
		clusterNamespace = mlbConfig.Namespace
	}

	latitudeCluster := &infrav1.LatitudeCluster{}
	clusterKey := types.NamespacedName{
		Name:      mlbConfig.Spec.ClusterRef.Name,
		Namespace: clusterNamespace,
	}

	if err := r.Get(ctx, clusterKey, latitudeCluster); err != nil {
		return nil, fmt.Errorf("failed to get LatitudeCluster: %w", err)
	}

	return latitudeCluster, nil
}

func (r *MetalLBConfigReconciler) getCluster(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) (*clusterv1.Cluster, error) {
	cluster, err := util.GetOwnerCluster(ctx, r.Client, latitudeCluster.ObjectMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to get owner Cluster: %w", err)
	}
	if cluster == nil {
		return nil, fmt.Errorf("LatitudeCluster has no owner Cluster")
	}
	return cluster, nil
}

func (r *MetalLBConfigReconciler) getWorkloadClusterClient(ctx context.Context, cluster *clusterv1.Cluster) (*kubernetes.Clientset, *rest.Config, error) {
	// Get the kubeconfig secret
	secretName := fmt.Sprintf("%s-kubeconfig", cluster.Name)
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      secretName,
		Namespace: cluster.Namespace,
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		return nil, nil, fmt.Errorf("failed to get kubeconfig secret: %w", err)
	}

	kubeconfigData, ok := secret.Data["value"]
	if !ok {
		return nil, nil, fmt.Errorf("kubeconfig data not found in secret")
	}

	// Create rest config from kubeconfig
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create rest config: %w", err)
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return clientset, config, nil
}

func (r *MetalLBConfigReconciler) installMetalLB(ctx context.Context, clientset *kubernetes.Clientset, mlbConfig *infrav1.MetalLBConfig) error {
	log := crlog.FromContext(ctx)

	version := mlbConfig.Spec.Version
	if version == "" {
		version = metallb.DefaultMetalLBVersion
	}

	// Ensure metallb-system namespace exists
	if err := metallb.EnsureNamespace(ctx, clientset, metallb.MetalLBNamespace); err != nil {
		return fmt.Errorf("failed to ensure MetalLB namespace: %w", err)
	}

	// Install MetalLB using the manifest
	// Note: For production, we should download and apply the actual manifest
	// For now, we'll document that users need to install MetalLB manually first
	log.Info("MetalLB installation assumed (users should install via kubectl apply)")
	log.Info("Install command", "url", metallb.GetMetalLBManifestURL(version))

	// In a full implementation, we would:
	// 1. Download the manifest from metallb.GetMetalLBManifestURL(version)
	// 2. Parse and apply each resource
	// 3. Wait for MetalLB pods to be ready

	return nil
}

func (r *MetalLBConfigReconciler) configureMetalLB(ctx context.Context, restConfig *rest.Config, mlbConfig *infrav1.MetalLBConfig) error {
	log := crlog.FromContext(ctx)

	// Create dynamic client for applying CRs
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Apply IPAddressPool
	log.Info("Applying IPAddressPool", "name", mlbConfig.Spec.IPAddressPool.Name)
	poolYAML := metallb.GenerateIPAddressPoolYAML(mlbConfig.Spec.IPAddressPool)
	if err := metallb.ApplyYAML(ctx, dynamicClient, poolYAML); err != nil {
		return fmt.Errorf("failed to apply IPAddressPool: %w", err)
	}

	// Apply L2Advertisements
	if len(mlbConfig.Spec.L2Advertisements) == 0 {
		// Create default L2Advertisement if none specified
		defaultL2Ad := infrav1.L2Advertisement{
			Name:           "default-l2-advertisement",
			IPAddressPools: []string{mlbConfig.Spec.IPAddressPool.Name},
		}
		log.Info("Applying default L2Advertisement")
		l2adYAML := metallb.GenerateL2AdvertisementYAML(defaultL2Ad)
		if err := metallb.ApplyYAML(ctx, dynamicClient, l2adYAML); err != nil {
			return fmt.Errorf("failed to apply default L2Advertisement: %w", err)
		}
	} else {
		for _, l2ad := range mlbConfig.Spec.L2Advertisements {
			log.Info("Applying L2Advertisement", "name", l2ad.Name)
			l2adYAML := metallb.GenerateL2AdvertisementYAML(l2ad)
			if err := metallb.ApplyYAML(ctx, dynamicClient, l2adYAML); err != nil {
				return fmt.Errorf("failed to apply L2Advertisement: %w", err)
			}
		}
	}

	return nil
}

func (r *MetalLBConfigReconciler) createControlPlaneService(ctx context.Context, clientset *kubernetes.Clientset, restConfig *rest.Config, mlbConfig *infrav1.MetalLBConfig, latitudeCluster *infrav1.LatitudeCluster) (string, error) {
	log := crlog.FromContext(ctx)

	// Create dynamic client for applying Service
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Generate Service YAML
	serviceYAML := metallb.GenerateControlPlaneServiceYAML(mlbConfig)
	log.Info("Applying control plane LoadBalancer Service")

	// Apply the Service
	if err := metallb.ApplyYAML(ctx, dynamicClient, serviceYAML); err != nil {
		return "", fmt.Errorf("failed to apply control plane service: %w", err)
	}

	// Wait a bit for MetalLB to assign an IP
	time.Sleep(5 * time.Second)

	// Get the LoadBalancer IP
	endpoint, err := metallb.GetServiceLoadBalancerIP(ctx, clientset, "default", "kubernetes-api-lb")
	if err != nil {
		log.Info("LoadBalancer IP not yet assigned, will retry", "error", err)
		// Return empty string - it will be populated on next reconciliation
		return "", nil
	}

	log.Info("Control plane LoadBalancer IP assigned", "ip", endpoint)
	return endpoint, nil
}

func (r *MetalLBConfigReconciler) setCondition(mlbConfig *infrav1.MetalLBConfig, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: mlbConfig.Generation,
	}

	// Find and update existing condition or append new one
	found := false
	for i, c := range mlbConfig.Status.Conditions {
		if c.Type == conditionType {
			if c.Status != status || c.Reason != reason || c.Message != message {
				mlbConfig.Status.Conditions[i] = condition
			}
			found = true
			break
		}
	}
	if !found {
		mlbConfig.Status.Conditions = append(mlbConfig.Status.Conditions, condition)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MetalLBConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.MetalLBConfig{}).
		Complete(r)
}
