package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/haproxy"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/latitude"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// RBAC for our CRDs
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines/status,verbs=get
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

const (
	LatitudeClusterFinalizerName = "latitudecluster.infrastructure.cluster.x-k8s.io"

	// Condition types
	ClusterReadyCondition      = "ClusterReady"
	LoadBalancerReadyCondition = "LoadBalancerReady"

	// Condition reasons
	ClusterProvisionFailedReason       = "ClusterProvisionFailed"
	ClusterNotReadyReason              = "ClusterNotReady"
	ClusterDeletionFailedReason        = "ClusterDeletionFailed"
	WaitingForInfrastructureReason     = "WaitingForInfrastructure"
	WaitingForControlPlaneReason       = "WaitingForControlPlane"
	InvalidClusterConfigReason         = "InvalidClusterConfig"
	ControlPlaneEndpointSetReason      = "ControlPlaneEndpointSet"
	ControlPlaneEndpointNotReadyReason = "ControlPlaneEndpointNotReady"
	LoadBalancerProvisioningReason     = "LoadBalancerProvisioning"
	LoadBalancerProvisionFailedReason  = "LoadBalancerProvisionFailed"
	LoadBalancerReadyReason            = "LoadBalancerReady"

	// Default values for LoadBalancer
	DefaultLoadBalancerPlan = "c2-small-x86"
	DefaultLoadBalancerOS   = "ubuntu_24_04_x64_lts"
	DefaultAPIPort          = 6443
	DefaultStatsPort        = 9000
)

type LatitudeClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	recorder       record.EventRecorder
	LatitudeClient latitude.ClientInterface
}

func (r *LatitudeClusterReconciler) SetRecorder(recorder record.EventRecorder) {
	r.recorder = recorder
}

func (r *LatitudeClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := crlog.FromContext(ctx).WithValues("latitudecluster", req.NamespacedName)
	log.Info("reconcile start")

	// Fetch the LatitudeCluster
	latitudeCluster := &infrav1.LatitudeCluster{}
	err := r.Get(ctx, req.NamespacedName, latitudeCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get the owner Cluster
	cluster, err := util.GetOwnerCluster(ctx, r.Client, latitudeCluster.ObjectMeta)
	if err != nil {
		log.Error(err, "failed to get owner Cluster")
		return ctrl.Result{}, err
	}
	if cluster == nil {
		log.Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	log = log.WithValues("cluster", cluster.Name)

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(latitudeCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to Patch the LatitudeCluster object and status after each reconciliation
	defer func() {
		if err := patchHelper.Patch(ctx, latitudeCluster); err != nil {
			log.Error(err, "failed to patch LatitudeCluster")
			if reterr == nil {
				reterr = err
			}
		}
	}()

	// Handle deleted clusters
	if !latitudeCluster.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, latitudeCluster)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, latitudeCluster, cluster)
}

func (r *LatitudeClusterReconciler) reconcileNormal(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(latitudeCluster, LatitudeClusterFinalizerName) {
		controllerutil.AddFinalizer(latitudeCluster, LatitudeClusterFinalizerName)
		return ctrl.Result{}, nil
	}

	// Validate cluster specification
	if err := r.validateClusterSpec(latitudeCluster); err != nil {
		log.Info("Invalid cluster spec", "error", err)
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, InvalidClusterConfigReason, err.Error())
		return ctrl.Result{}, nil
	}

	// Reconcile cluster infrastructure (networks, firewalls, etc)
	if err := r.reconcileInfrastructure(ctx, latitudeCluster); err != nil {
		log.Error(err, "failed to reconcile cluster infrastructure")
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ClusterProvisionFailedReason, err.Error())
		r.recorder.Eventf(latitudeCluster, corev1.EventTypeWarning, "FailedInfrastructure", "Failed to setup cluster infrastructure: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Mark cluster infrastructure as ready immediately after validation
	// This allows KubeadmControlPlane to start creating machines
	if !latitudeCluster.Status.Ready {
		latitudeCluster.Status.Ready = true
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionTrue, "ClusterReady", "Cluster infrastructure is ready")
		log.Info("Marked cluster as ready, KubeadmControlPlane can now create machines")
		return ctrl.Result{}, nil
	}

	// After cluster is ready, try to discover control plane endpoint from machines
	// This is optional and happens asynchronously
	endpointSet, err := r.setupControlPlaneEndpoint(ctx, latitudeCluster, cluster)
	if err != nil {
		log.V(1).Info("Control plane endpoint not discovered yet", "error", err)
		// Don't return error - this is expected until machines are ready
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	if !endpointSet {
		log.V(1).Info("Waiting for control plane machine to get IP address")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Endpoint discovered successfully
	log.Info("Control plane endpoint discovered and set")

	// Ensure Cluster's control plane endpoint is also set
	if err := r.ensureClusterEndpoint(ctx, latitudeCluster, cluster); err != nil {
		log.Error(err, "failed to ensure cluster control plane endpoint")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "SuccessfulInfrastructure", "Cluster infrastructure is ready with endpoint %s:%d",
		latitudeCluster.Status.ControlPlaneEndpoint.Host,
		latitudeCluster.Status.ControlPlaneEndpoint.Port)
	log.Info("Successfully reconciled LatitudeCluster",
		"endpoint", fmt.Sprintf("%s:%d", latitudeCluster.Status.ControlPlaneEndpoint.Host, latitudeCluster.Status.ControlPlaneEndpoint.Port))

	return ctrl.Result{}, nil
}

//nolint:unparam // error may be used in future
func (r *LatitudeClusterReconciler) reconcileDelete(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Cleanup cluster infrastructure
	if err := r.cleanupInfrastructure(ctx, latitudeCluster); err != nil {
		log.Error(err, "failed to cleanup cluster infrastructure")
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ClusterDeletionFailedReason, err.Error())
		r.recorder.Eventf(latitudeCluster, corev1.EventTypeWarning, "FailedCleanup", "Failed to cleanup cluster infrastructure: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(latitudeCluster, LatitudeClusterFinalizerName)
	log.Info("Successfully deleted LatitudeCluster")

	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "SuccessfulCleanup", "Cluster infrastructure cleaned up")

	return ctrl.Result{}, nil
}

func (r *LatitudeClusterReconciler) validateClusterSpec(latitudeCluster *infrav1.LatitudeCluster) error {
	var errors []string

	// Validate required fields
	if latitudeCluster.Spec.Location == "" {
		errors = append(errors, "location is required")
	}

	if latitudeCluster.Spec.ProjectRef == nil || latitudeCluster.Spec.ProjectRef.ProjectID == "" {
		errors = append(errors, "projectRef.projectID is required")
	}

	// Control plane endpoint will be set dynamically, so we don't validate it here

	if len(errors) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(errors, ", "))
	}
	return nil
}

func (r *LatitudeClusterReconciler) reconcileInfrastructure(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) error {
	log := crlog.FromContext(ctx)

	// Validate that the location/region exists
	regions, err := r.LatitudeClient.GetAvailableRegions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get available regions: %w", err)
	}

	found := false
	for _, region := range regions {
		if region == latitudeCluster.Spec.Location {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("location %s is not available, available regions: %v", latitudeCluster.Spec.Location, regions)
	}

	// Provision load balancer if enabled
	if latitudeCluster.Spec.LoadBalancer != nil && latitudeCluster.Spec.LoadBalancer.Enabled {
		if err := r.reconcileLoadBalancer(ctx, latitudeCluster); err != nil {
			log.Error(err, "failed to reconcile load balancer")
			return fmt.Errorf("failed to reconcile load balancer: %w", err)
		}
	}

	log.Info("Cluster infrastructure setup completed", "location", latitudeCluster.Spec.Location)

	return nil
}

func (r *LatitudeClusterReconciler) cleanupInfrastructure(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) error {
	log := crlog.FromContext(ctx)

	// Cleanup load balancer if it exists
	if latitudeCluster.Status.LoadBalancer != nil && latitudeCluster.Status.LoadBalancer.ServerID != "" {
		log.Info("Deleting load balancer server", "serverID", latitudeCluster.Status.LoadBalancer.ServerID)

		if err := r.LatitudeClient.DeleteServer(ctx, latitudeCluster.Status.LoadBalancer.ServerID); err != nil {
			log.Error(err, "failed to delete load balancer server", "serverID", latitudeCluster.Status.LoadBalancer.ServerID)
			// Don't fail the cleanup if the server is already gone
			if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "404") {
				return fmt.Errorf("failed to delete load balancer server: %w", err)
			}
			log.Info("Load balancer server already deleted or not found")
		} else {
			log.Info("Load balancer server deleted successfully")
			r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "LoadBalancerDeleted",
				"Load balancer server deleted: %s", latitudeCluster.Status.LoadBalancer.ServerID)
		}
	}

	// TODO: Implement additional infrastructure cleanup:
	// - Remove private networks
	// - Cleanup firewalls
	// - Cleanup SSH keys if created

	log.Info("Cluster infrastructure cleanup completed")
	return nil
}

//nolint:unparam // conditionType may vary in future
func (r *LatitudeClusterReconciler) setCondition(latitudeCluster *infrav1.LatitudeCluster, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Find existing condition
	for i, existingCondition := range latitudeCluster.Status.Conditions {
		if existingCondition.Type == conditionType {
			// Only update if status or reason changed to avoid unnecessary updates
			if existingCondition.Status != status || existingCondition.Reason != reason {
				latitudeCluster.Status.Conditions[i] = condition
			}
			return
		}
	}

	// Add new condition
	latitudeCluster.Status.Conditions = append(latitudeCluster.Status.Conditions, condition)
}

// setupControlPlaneEndpoint discovers the first ready control plane machine and sets the endpoint
// Returns (endpointSet bool, error)
func (r *LatitudeClusterReconciler) setupControlPlaneEndpoint(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster, cluster *clusterv1.Cluster) (bool, error) {
	log := crlog.FromContext(ctx)

	// If control plane endpoint is already set, nothing to do
	if latitudeCluster.Status.ControlPlaneEndpoint.Host != "" {
		log.V(1).Info("Control plane endpoint already set",
			"host", latitudeCluster.Status.ControlPlaneEndpoint.Host,
			"port", latitudeCluster.Status.ControlPlaneEndpoint.Port)
		return true, nil
	}

	// List all CAPI Machines that belong to this cluster
	machineList := &clusterv1.MachineList{}
	if err := r.List(ctx, machineList,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{
			clusterv1.ClusterNameLabel: cluster.Name,
		}); err != nil {
		return false, fmt.Errorf("failed to list CAPI machines: %w", err)
	}

	if len(machineList.Items) == 0 {
		log.Info("No machines found yet, waiting for machine controller to create them")
		return false, nil
	}

	// Find the first control plane machine that has an InfrastructureRef
	var controlPlaneMachine *clusterv1.Machine
	for i := range machineList.Items {
		machine := &machineList.Items[i]
		if util.IsControlPlaneMachine(machine) {
			controlPlaneMachine = machine
			log.Info("Found control plane machine",
				"machine", machine.Name,
				"phase", machine.Status.Phase)
			break
		}
	}

	if controlPlaneMachine == nil {
		log.Info("No control plane machine found yet")
		return false, nil
	}

	// Get the LatitudeMachine referenced by the control plane Machine
	if controlPlaneMachine.Spec.InfrastructureRef.Name == "" {
		log.Info("Control plane machine has no infrastructure ref yet")
		return false, nil
	}

	latitudeMachine := &infrav1.LatitudeMachine{}
	latitudeMachineKey := client.ObjectKey{
		Namespace: controlPlaneMachine.Spec.InfrastructureRef.Namespace,
		Name:      controlPlaneMachine.Spec.InfrastructureRef.Name,
	}

	if err := r.Get(ctx, latitudeMachineKey, latitudeMachine); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("LatitudeMachine not found yet", "key", latitudeMachineKey)
			return false, nil
		}
		return false, fmt.Errorf("failed to get LatitudeMachine: %w", err)
	}

	// Check if the LatitudeMachine is ready and has an IP address
	if !latitudeMachine.Status.Ready {
		log.Info("LatitudeMachine is not ready yet",
			"machine", latitudeMachine.Name,
			"ready", latitudeMachine.Status.Ready)
		return false, nil
	}

	// Find an external IP address
	var externalIP string
	for _, addr := range latitudeMachine.Status.Addresses {
		// MachineAddress types: InternalIP, ExternalIP, InternalDNS, ExternalDNS, Hostname
		if addr.Type == clusterv1.MachineInternalIP || addr.Type == "ExternalIP" {
			externalIP = addr.Address
			break
		}
	}

	// Fallback: if no ExternalIP, try InternalIP (for private networks)
	if externalIP == "" {
		for _, addr := range latitudeMachine.Status.Addresses {
			if addr.Type == clusterv1.MachineInternalIP || addr.Type == "InternalIP" {
				externalIP = addr.Address
				log.Info("Using InternalIP as fallback", "ip", externalIP)
				break
			}
		}
	}

	if externalIP == "" {
		log.Info("LatitudeMachine is ready but has no external IP yet", "machine", latitudeMachine.Name)
		return false, fmt.Errorf("control plane machine %s has no external IP address", latitudeMachine.Name)
	}

	// Set the control plane endpoint
	latitudeCluster.Status.ControlPlaneEndpoint = infrav1.APIEndpoint{
		Host: externalIP,
		Port: 6443, // Default Kubernetes API server port
	}

	log.Info("Successfully set control plane endpoint",
		"host", externalIP,
		"port", 6443,
		"from-machine", latitudeMachine.Name)

	r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ControlPlaneEndpointSetReason,
		fmt.Sprintf("Control plane endpoint set to %s:6443", externalIP))

	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "ControlPlaneEndpointSet",
		"Control plane endpoint set to %s:6443 from machine %s", externalIP, latitudeMachine.Name)

	return true, nil
}

// ensureClusterEndpoint ensures the parent Cluster resource has the control plane endpoint set
func (r *LatitudeClusterReconciler) ensureClusterEndpoint(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster, cluster *clusterv1.Cluster) error {
	log := crlog.FromContext(ctx)

	// Check if LatitudeCluster has a control plane endpoint set
	if latitudeCluster.Status.ControlPlaneEndpoint.Host == "" {
		log.V(1).Info("LatitudeCluster has no control plane endpoint set yet")
		return nil
	}

	// Check if Cluster already has the endpoint set
	if cluster.Spec.ControlPlaneEndpoint.Host != "" {
		log.V(1).Info("Cluster control plane endpoint already set",
			"host", cluster.Spec.ControlPlaneEndpoint.Host,
			"port", cluster.Spec.ControlPlaneEndpoint.Port)
		return nil
	}

	// Update Cluster's control plane endpoint
	patchHelper, err := patch.NewHelper(cluster, r.Client)
	if err != nil {
		return fmt.Errorf("failed to create patch helper for cluster: %w", err)
	}

	cluster.Spec.ControlPlaneEndpoint.Host = latitudeCluster.Status.ControlPlaneEndpoint.Host
	cluster.Spec.ControlPlaneEndpoint.Port = int32(latitudeCluster.Status.ControlPlaneEndpoint.Port)

	if err := patchHelper.Patch(ctx, cluster); err != nil {
		log.Error(err, "failed to patch cluster control plane endpoint")
		return fmt.Errorf("failed to patch cluster: %w", err)
	}

	log.Info("Updated Cluster control plane endpoint",
		"host", cluster.Spec.ControlPlaneEndpoint.Host,
		"port", cluster.Spec.ControlPlaneEndpoint.Port)

	r.recorder.Eventf(cluster, corev1.EventTypeNormal, "ControlPlaneEndpointSet",
		"Control plane endpoint set to %s:%d", cluster.Spec.ControlPlaneEndpoint.Host, cluster.Spec.ControlPlaneEndpoint.Port)

	return nil
}

// reconcileLoadBalancer provisions and manages the HAProxy load balancer
func (r *LatitudeClusterReconciler) reconcileLoadBalancer(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) error {
	log := crlog.FromContext(ctx)

	// Initialize status if needed
	if latitudeCluster.Status.LoadBalancer == nil {
		latitudeCluster.Status.LoadBalancer = &infrav1.LoadBalancerStatus{}
	}

	// If load balancer is already provisioned and ready, check if we need to update backends
	if latitudeCluster.Status.LoadBalancer.ServerID != "" && latitudeCluster.Status.LoadBalancer.Ready {
		log.V(1).Info("Load balancer already provisioned", "serverID", latitudeCluster.Status.LoadBalancer.ServerID)
		// TODO: Update backends if control planes changed
		return nil
	}

	// If we already have a serverID but it's not ready, check its status
	if latitudeCluster.Status.LoadBalancer.ServerID != "" {
		server, err := r.LatitudeClient.GetServer(ctx, latitudeCluster.Status.LoadBalancer.ServerID)
		if err != nil {
			log.Error(err, "failed to get load balancer server status")
			r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
				LoadBalancerProvisionFailedReason, fmt.Sprintf("Failed to get server status: %v", err))
			return err
		}

		// Check if server is ready
		status := strings.ToLower(server.Status)
		if status == "on" || status == "active" || status == "running" {
			// Server is ready, update status
			latitudeCluster.Status.LoadBalancer.Ready = true

			// Get primary IP
			var primaryIP string
			if len(server.IPAddress) > 0 {
				primaryIP = server.IPAddress[0]
			}

			if primaryIP == "" {
				log.Info("Server is ready but has no IP address yet")
				return fmt.Errorf("server is ready but has no IP address")
			}

			latitudeCluster.Status.LoadBalancer.InternalIP = primaryIP
			latitudeCluster.Status.LoadBalancer.PublicIP = primaryIP

			// Set control plane endpoint to load balancer IP
			apiPort := latitudeCluster.Spec.LoadBalancer.Port
			if apiPort == 0 {
				apiPort = DefaultAPIPort
			}
			latitudeCluster.Status.ControlPlaneEndpoint = infrav1.APIEndpoint{
				Host: primaryIP,
				Port: apiPort,
			}

			r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionTrue,
				LoadBalancerReadyReason, "Load balancer is ready")
			r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "LoadBalancerReady",
				"Load balancer is ready at %s:%d", primaryIP, apiPort)

			log.Info("Load balancer is ready", "ip", primaryIP, "port", apiPort)
			return nil
		}

		// Server is not ready yet
		log.Info("Load balancer server is not ready yet", "status", server.Status)
		r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
			LoadBalancerProvisioningReason, fmt.Sprintf("Server status: %s", server.Status))
		return fmt.Errorf("load balancer server not ready, status: %s", server.Status)
	}

	// Provision new load balancer
	log.Info("Provisioning new HAProxy load balancer")

	// Set defaults
	plan := latitudeCluster.Spec.LoadBalancer.Plan
	if plan == "" {
		plan = DefaultLoadBalancerPlan
	}

	os := latitudeCluster.Spec.LoadBalancer.OperatingSystem
	if os == "" {
		os = DefaultLoadBalancerOS
	}

	apiPort := latitudeCluster.Spec.LoadBalancer.Port
	if apiPort == 0 {
		apiPort = DefaultAPIPort
	}

	statsPort := latitudeCluster.Spec.LoadBalancer.StatsPort
	if statsPort == 0 {
		statsPort = DefaultStatsPort
	}

	// Generate HAProxy cloud-init with empty backends (will be added later)
	haproxyConfig := haproxy.Config{
		APIPort:      apiPort,
		StatsPort:    statsPort,
		Backends:     []haproxy.Backend{}, // Empty initially
		EnableStats:  statsPort > 0,
		UpdateScript: true,
	}

	userDataBase64, err := haproxy.GenerateCloudInitBase64(haproxyConfig)
	if err != nil {
		log.Error(err, "failed to generate HAProxy cloud-init")
		r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
			LoadBalancerProvisionFailedReason, fmt.Sprintf("Failed to generate cloud-init: %v", err))
		return fmt.Errorf("failed to generate cloud-init: %w", err)
	}

	// Upload userdata to Latitude
	userDataID, err := r.LatitudeClient.CreateUserData(ctx, latitude.CreateUserDataRequest{
		Name:    fmt.Sprintf("%s-lb-userdata", latitudeCluster.Name),
		Content: userDataBase64,
	})
	if err != nil {
		log.Error(err, "failed to upload load balancer userdata")
		r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
			LoadBalancerProvisionFailedReason, fmt.Sprintf("Failed to upload userdata: %v", err))
		return fmt.Errorf("failed to upload userdata: %w", err)
	}

	log.Info("Uploaded load balancer userdata", "userDataID", userDataID)

	// Create load balancer server
	serverSpec := latitude.ServerSpec{
		Project:         latitudeCluster.Spec.ProjectRef.ProjectID,
		Site:            latitudeCluster.Spec.Location,
		Plan:            plan,
		OperatingSystem: os,
		Hostname:        fmt.Sprintf("%s-lb", latitudeCluster.Name),
		UserData:        userDataID,
	}

	server, err := r.LatitudeClient.CreateServer(ctx, serverSpec)
	if err != nil {
		log.Error(err, "failed to create load balancer server")
		r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
			LoadBalancerProvisionFailedReason, fmt.Sprintf("Failed to create server: %v", err))
		r.recorder.Eventf(latitudeCluster, corev1.EventTypeWarning, "LoadBalancerProvisionFailed",
			"Failed to create load balancer: %v", err)
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Update status
	latitudeCluster.Status.LoadBalancer.ServerID = server.ID
	latitudeCluster.Status.LoadBalancer.Ready = false

	r.setCondition(latitudeCluster, LoadBalancerReadyCondition, metav1.ConditionFalse,
		LoadBalancerProvisioningReason, fmt.Sprintf("Load balancer server created: %s", server.ID))
	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "LoadBalancerProvisioning",
		"Load balancer server created: %s", server.ID)

	log.Info("Load balancer server created", "serverID", server.ID, "hostname", server.Hostname)

	return fmt.Errorf("load balancer is provisioning, waiting for server to be ready")
}

func (r *LatitudeClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudecluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeCluster{}).
		Complete(r)
}
