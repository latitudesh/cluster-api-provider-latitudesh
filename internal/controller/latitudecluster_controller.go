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
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

const (
	LatitudeClusterFinalizerName = "latitudecluster.infrastructure.cluster.x-k8s.io"

	// Condition types
	ClusterReadyCondition = "ClusterReady"

	// Condition reasons
	ClusterProvisionFailedReason   = "ClusterProvisionFailed"
	ClusterNotReadyReason          = "ClusterNotReady"
	ClusterDeletionFailedReason    = "ClusterDeletionFailed"
	WaitingForInfrastructureReason = "WaitingForInfrastructure"
	InvalidClusterConfigReason     = "InvalidClusterConfig"
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
	return r.reconcileNormal(ctx, latitudeCluster)
}

func (r *LatitudeClusterReconciler) reconcileNormal(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(latitudeCluster, LatitudeClusterFinalizerName) {
		controllerutil.AddFinalizer(latitudeCluster, LatitudeClusterFinalizerName)
		return ctrl.Result{}, nil
	}

	// If already ready, do nothing
	if latitudeCluster.Status.Ready {
		return ctrl.Result{}, nil
	}

	// Validate cluster specification
	if err := r.validateClusterSpec(latitudeCluster); err != nil {
		log.Info("Invalid cluster spec", "error", err)
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ClusterProvisionFailedReason, err.Error())
		return ctrl.Result{}, nil
	}

	// Reconcile cluster infrastructure
	if err := r.reconcileInfrastructure(ctx, latitudeCluster); err != nil {
		log.Error(err, "failed to reconcile cluster infrastructure")
		r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionFalse, ClusterProvisionFailedReason, err.Error())
		r.recorder.Eventf(latitudeCluster, corev1.EventTypeWarning, "FailedInfrastructure", "Failed to setup cluster infrastructure: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Setup control plane endpoint if specified
	// if latitudeCluster.Spec.ControlPlaneEndpoint.Host != "" {
	// 	latitudeCluster.Status.ControlPlaneEndpoint = latitudeCluster.Spec.ControlPlaneEndpoint
	// }

	// Mark cluster as ready
	latitudeCluster.Status.Ready = true
	r.setCondition(latitudeCluster, ClusterReadyCondition, metav1.ConditionTrue, "ClusterReady", "Cluster infrastructure is ready")

	r.recorder.Eventf(latitudeCluster, corev1.EventTypeNormal, "SuccessfulInfrastructure", "Cluster infrastructure is ready")
	log.Info("Successfully reconciled LatitudeCluster")

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

	// Validate control plane endpoint if specified
	// if latitudeCluster.Spec.ControlPlaneEndpoint.Host != "" {
	// 	if latitudeCluster.Spec.ControlPlaneEndpoint.Port <= 0 {
	// 		errors = append(errors, "controlPlaneEndpoint.port must be greater than 0 when host is specified")
	// 	}
	// }

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
	// - Configure load balancers
	// - Prepare SSH keys

	return nil
}

func (r *LatitudeClusterReconciler) cleanupInfrastructure(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) error {
	log := crlog.FromContext(ctx)

	// TODO: Implement infrastructure cleanup:
	// - Remove private networks
	// - Cleanup firewalls
	// - Remove load balancers
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
			latitudeCluster.Status.Conditions[i] = condition
			return
		}
	}

	// Add new condition
	latitudeCluster.Status.Conditions = append(latitudeCluster.Status.Conditions, condition)
}

func (r *LatitudeClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudecluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeCluster{}).
		Complete(r)
}
