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
	ClusterReadyCondition = "ClusterReady"

	// Condition reasons
	ClusterProvisionFailedReason       = "ClusterProvisionFailed"
	ClusterNotReadyReason              = "ClusterNotReady"
	ClusterDeletionFailedReason        = "ClusterDeletionFailed"
	WaitingForInfrastructureReason     = "WaitingForInfrastructure"
	WaitingForControlPlaneReason       = "WaitingForControlPlane"
	InvalidClusterConfigReason         = "InvalidClusterConfig"
	ControlPlaneEndpointSetReason      = "ControlPlaneEndpointSet"
	ControlPlaneEndpointNotReadyReason = "ControlPlaneEndpointNotReady"
)

type LatitudeClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	recorder       record.EventRecorder
	LatitudeClient *latitude.Client
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
	if !latitudeCluster.ObjectMeta.DeletionTimestamp.IsZero() {
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

	// Setup control plane endpoint dynamically from first ready control plane machine
	endpointSet, err := r.setupControlPlaneEndpoint(ctx, latitudeCluster, cluster)
	if err != nil {
		log.Error(err, "failed to setup control plane endpoint")
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ClusterNotReadyReason, err.Error())
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If endpoint is not ready yet, wait for control plane machine to be provisioned
	if !endpointSet {
		log.Info("Waiting for control plane machine to be ready")
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, WaitingForControlPlaneReason, "Waiting for control plane machine to be provisioned and ready")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// If already ready and endpoint is set, nothing more to do
	if latitudeCluster.Status.Ready {
		return ctrl.Result{}, nil
	}

	// Mark cluster as ready
	latitudeCluster.Status.Ready = true
	r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionTrue, "ClusterReady", "Cluster infrastructure is ready")

	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "SuccessfulInfrastructure", "Cluster infrastructure is ready with endpoint %s:%d",
		latitudeCluster.Status.ControlPlaneEndpoint.Host,
		latitudeCluster.Status.ControlPlaneEndpoint.Port)
	log.Info("Successfully reconciled LatitudeCluster",
		"endpoint", fmt.Sprintf("%s:%d", latitudeCluster.Status.ControlPlaneEndpoint.Host, latitudeCluster.Status.ControlPlaneEndpoint.Port))

	return ctrl.Result{}, nil
}

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

	log.Info("Cluster infrastructure setup completed", "location", latitudeCluster.Spec.Location)

	// TODO: Implement additional infrastructure setup if needed:
	// - Create private networks
	// - Setup firewalls
	// - Configure load balancers (if supported)
	// - Prepare SSH keys

	return nil
}

func (r *LatitudeClusterReconciler) cleanupInfrastructure(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) error {
	log := crlog.FromContext(ctx)

	// TODO: Implement infrastructure cleanup:
	// - Remove private networks
	// - Cleanup firewalls
	// - Remove load balancers (if created)
	// - Cleanup SSH keys if created

	log.Info("Cluster infrastructure cleanup completed")
	return nil
}

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
		if addr.Type == string(clusterv1.MachineExternalIP) || addr.Type == "ExternalIP" {
			externalIP = addr.Address
			break
		}
	}

	// Fallback: if no ExternalIP, try InternalIP (for private networks)
	if externalIP == "" {
		for _, addr := range latitudeMachine.Status.Addresses {
			if addr.Type == string(clusterv1.MachineInternalIP) || addr.Type == "InternalIP" {
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

func (r *LatitudeClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudecluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeCluster{}).
		Complete(r)
}
