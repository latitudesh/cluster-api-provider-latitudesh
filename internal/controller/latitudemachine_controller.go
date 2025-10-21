package controllers

import (
	"context"
	"encoding/base64"
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
	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;clusters;machinesets;machinedeployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines/status,verbs=get
// +kubebuilder:rbac:groups=bootstrap.cluster.x-k8s.io,resources=kubeadmconfigs;kubeadmconfigtemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

const (
	LatitudeFinalizerName = "latitudemachine.infrastructure.cluster.x-k8s.io"
)

type LatitudeMachineReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	recorder       record.EventRecorder
	LatitudeClient *latitude.Client
}

func (r *LatitudeMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := crlog.FromContext(ctx).WithValues("latitudemachine", req.NamespacedName)
	log.Info("reconcile start")

	latitudeMachine := &infrav1.LatitudeMachine{}
	err := r.Get(ctx, req.NamespacedName, latitudeMachine)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(latitudeMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to Patch the LatitudeMachine object and status after each reconciliation
	defer func() {
		if err := patchHelper.Patch(ctx, latitudeMachine, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "failed to patch LatitudeMachine")
			if reterr == nil {
				reterr = err
			}
		}
	}()

	if _, paused := latitudeMachine.Annotations["cluster.x-k8s.io/paused"]; paused {
		log.Info("resource is paused; skipping")
		return ctrl.Result{}, nil
	}

	// Handle deleted machines
	if !latitudeMachine.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, latitudeMachine)
	}

	// Handle non-deleted machines
	return r.reconcileNormal(ctx, latitudeMachine, patchHelper)
}

func (r *LatitudeMachineReconciler) reconcileNormal(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine, patchHelper *patch.Helper) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(latitudeMachine, LatitudeFinalizerName) {
		controllerutil.AddFinalizer(latitudeMachine, LatitudeFinalizerName)
		return ctrl.Result{}, nil
	}

	// If already ready, do nothing
	if latitudeMachine.Status.Ready {
		return ctrl.Result{}, nil
	}

	// Check if we have required fields
	if err := r.validateMachineSpec(ctx, latitudeMachine); err != nil {
		log.Info("Invalid machine spec", "error", err)
		r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceProvisionFailedReason, err.Error())
		return ctrl.Result{}, nil
	}

	// Create or get server from Latitude.sh
	server, err := r.reconcileServer(ctx, latitudeMachine, patchHelper)
	if err != nil {
		log.Error(err, "failed to reconcile server")
		r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceProvisionFailedReason, err.Error())
		r.recorder.Eventf(latitudeMachine, corev1.EventTypeWarning, "FailedCreate", "Failed to create server: %v", err)
		return ctrl.Result{}, err
	}

	if server == nil {
		log.Info("Server not ready yet")
		r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceNotReadyReason, "Server is being provisioned")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	pid := fmt.Sprintf("latitude://%s", server.ID)

	latitudeMachine.Spec.ProviderID = &pid

	// Update machine status
	latitudeMachine.Status.Ready = true
	latitudeMachine.Status.ProviderID = pid
	latitudeMachine.Status.ServerID = server.ID

	addresses := []clusterv1.MachineAddress{}

	for _, ip := range server.IPAddress {
		addresses = append(addresses, clusterv1.MachineAddress{
			Type:    clusterv1.MachineExternalIP,
			Address: ip,
		})
	}

	if server.Hostname != "" {
		addresses = append(addresses, clusterv1.MachineAddress{
			Type:    clusterv1.MachineHostName,
			Address: server.Hostname,
		})
	}

	latitudeMachine.Status.Addresses = addresses

	log.Info("Set machine addresses", "addresses", addresses)

	// Set instance ready condition
	r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionTrue, "InstanceReady", "Instance is ready")

	r.recorder.Eventf(latitudeMachine, corev1.EventTypeNormal, "SuccessfulCreate", "Created server %s", server.ID)
	log.Info("Successfully reconciled LatitudeMachine", "serverID", server.ID)

	return ctrl.Result{}, nil
}

func (r *LatitudeMachineReconciler) reconcileDelete(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	if latitudeMachine.Status.ServerID != "" {
		// Delete server from Latitude.sh
		err := r.LatitudeClient.DeleteServer(ctx, latitudeMachine.Status.ServerID)
		if err != nil {
			log.Error(err, "failed to delete server", "serverID", latitudeMachine.Status.ServerID)
			r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceDeletionFailedReason, err.Error())
			r.recorder.Eventf(latitudeMachine, corev1.EventTypeWarning, "FailedDelete", "Failed to delete server %s: %v", latitudeMachine.Status.ServerID, err)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		r.recorder.Eventf(latitudeMachine, corev1.EventTypeNormal, "SuccessfulDelete", "Deleted server %s", latitudeMachine.Status.ServerID)
	}

	if latitudeMachine.Status.UserDataID != "" {
		err := r.LatitudeClient.DeleteUserData(ctx, latitudeMachine.Status.UserDataID)
		if err != nil {
			log.Error(err, "failed to delete user data", "userDataID", latitudeMachine.Status.UserDataID)
			r.recorder.Eventf(latitudeMachine, corev1.EventTypeWarning, "FailedDeleteUserData", "Failed to delete user data %s: %v", latitudeMachine.Status.UserDataID, err)
		} else {
			r.recorder.Eventf(latitudeMachine, corev1.EventTypeNormal, "SuccessfulDeleteUserData", "Deleted user data %s", latitudeMachine.Status.UserDataID)
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(latitudeMachine, LatitudeFinalizerName)
	log.Info("Successfully deleted LatitudeMachine")

	return ctrl.Result{}, nil
}

func (r *LatitudeMachineReconciler) reconcileServer(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine, patchHelper *patch.Helper) (*latitude.Server, error) {
	log := crlog.FromContext(ctx)

	// metrics for reconcile server
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.Info("Reconcile server duration", "duration", duration)
	}()

	// If server already exists, check its status
	if latitudeMachine.Status.ServerID != "" {
		server, err := r.LatitudeClient.GetServer(ctx, latitudeMachine.Status.ServerID)
		if err != nil {
			// Server might have been deleted externally
			log.Info("Server not found, will create new one", "serverID", latitudeMachine.Status.ServerID)
			latitudeMachine.Status.ServerID = ""
		} else {
			// Check if server is ready
			if strings.EqualFold(server.Status, "on") ||
				strings.EqualFold(server.Status, "active") ||
				strings.EqualFold(server.Status, "running") {
				return server, nil
			}
			// Server exists but not ready yet
			log.Info("Server exists but not ready", "status", server.Status)
			return nil, nil
		}
	}

	userData, err := r.getBootstrapUserData(ctx, latitudeMachine)
	if err != nil {
		return nil, err
	}
	if userData == "" {
		// not ready -> caller requeue
		log.Info("Bootstrap user data not ready yet")
		return nil, nil
	}

	var udID string
	if latitudeMachine.Status.UserDataID != "" {
		udID = latitudeMachine.Status.UserDataID
		log.Info("Reusing existing user data", "userDataID", udID)
	} else {
		// Create new user data
		encoded := base64.StdEncoding.EncodeToString([]byte(userData))
		udID, err = r.LatitudeClient.CreateUserData(ctx, latitude.CreateUserDataRequest{
			Name:    fmt.Sprintf("%s-%s", latitudeMachine.Name, latitudeMachine.UID),
			Content: encoded,
		})
		if err != nil {
			return nil, fmt.Errorf("create user-data: %w", err)
		}

		// Store user data ID in status
		latitudeMachine.Status.UserDataID = udID
		log.Info("Created new user data", "userDataID", udID)

		// Persist the UserDataID immediately so we don't recreate it on retry
		if err := patchHelper.Patch(ctx, latitudeMachine, patch.WithStatusObservedGeneration{}); err != nil {
			log.Error(err, "failed to persist status after CreateUserData")
		}
	}

	// Create new server
	spec := latitude.ServerSpec{
		Project:         r.getProjectID(ctx, latitudeMachine),
		Plan:            latitudeMachine.Spec.Plan,
		OperatingSystem: latitudeMachine.Spec.OperatingSystem,
		Site:            r.getSite(ctx, latitudeMachine),
		Hostname:        r.getHostname(latitudeMachine),
		SSHKeys:         latitudeMachine.Spec.SSHKeys,
		UserData:        udID,
	}

	server, err := r.LatitudeClient.CreateServer(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	latitudeMachine.Status.ServerID = server.ID
	log.Info("Created server", "serverID", server.ID, "duration", time.Since(start))

	if err := patchHelper.Patch(ctx, latitudeMachine, patch.WithStatusObservedGeneration{}); err != nil {
		log.Error(err, "failed to persist status after CreateServer")
	}

	return nil, nil
}

func (r *LatitudeMachineReconciler) validateMachineSpec(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) error {
	var errors []string

	if latitudeMachine.Spec.OperatingSystem == "" {
		errors = append(errors, "operatingSystem is required")
	}
	if latitudeMachine.Spec.Plan == "" {
		errors = append(errors, "plan is required")
	}
	if r.getProjectID(ctx, latitudeMachine) == "" {
		errors = append(errors, "projectID is required")
	}
	if r.getSite(ctx, latitudeMachine) == "" {
		errors = append(errors, "site is required")
	}

	if len(errors) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(errors, ", "))
	}
	return nil
}

func (r *LatitudeMachineReconciler) getProjectID(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) string {
	cluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil {
		return ""
	}
	if cluster.Spec.ProjectRef != nil {
		return cluster.Spec.ProjectRef.ProjectID
	}
	return ""
}

func (r *LatitudeMachineReconciler) getSite(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) string {
	log := crlog.FromContext(ctx)

	// Get Owner Machine
	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, latitudeMachine.ObjectMeta)
	if err != nil {
		log.Error(err, "failed to get owner Machine")
		return ""
	}

	if ownerMachine != nil && ownerMachine.Spec.FailureDomain != nil {
		return *ownerMachine.Spec.FailureDomain
	}

	cluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil {
		return ""
	}

	return cluster.Spec.Location
}

func (r *LatitudeMachineReconciler) getLatitudeCluster(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) (*infrav1.LatitudeCluster, error) {
	log := crlog.FromContext(ctx)

	// Get owner Machine
	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, latitudeMachine.ObjectMeta)
	if err != nil {
		return nil, fmt.Errorf("get owner Machine: %w", err)
	}
	if ownerMachine == nil {
		log.Info("Owner Machine not definied; requeue")
		return nil, nil
	}

	// Get cluster from Machine
	cluster, err := capiutil.GetClusterFromMetadata(ctx, r.Client, ownerMachine.ObjectMeta)
	if err != nil {
		return nil, fmt.Errorf("get cluster from Machine: %w", err)
	}
	if cluster == nil {
		log.Info("Cluster not definied; requeue")
		return nil, nil
	}

	// Get LatitudeCluster
	latitudeCluster := &infrav1.LatitudeCluster{}
	key := client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	if err := r.Client.Get(ctx, key, latitudeCluster); err != nil {
		return nil, fmt.Errorf("get LatitudeCluster: %w", err)
	}

	return latitudeCluster, nil
}

func (r *LatitudeMachineReconciler) getHostname(latitudeMachine *infrav1.LatitudeMachine) string {
	return fmt.Sprintf("%s-%s", latitudeMachine.Namespace, latitudeMachine.Name)
}

func (r *LatitudeMachineReconciler) setCondition(
	latitudeMachine *infrav1.LatitudeMachine,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: latitudeMachine.GetGeneration(),
	}

	conditions := latitudeMachine.Status.Conditions
	for i := range conditions {
		if conditions[i].Type != conditionType {
			continue
		}
		changed := conditions[i].Status != condition.Status ||
			conditions[i].Reason != condition.Reason ||
			conditions[i].Message != condition.Message

		conditions[i].Status = condition.Status
		conditions[i].Reason = condition.Reason
		conditions[i].Message = condition.Message
		conditions[i].ObservedGeneration = condition.ObservedGeneration
		if changed {
			conditions[i].LastTransitionTime = metav1.Now()
		}
		latitudeMachine.Status.Conditions = conditions
		return
	}

	condition.LastTransitionTime = metav1.Now()
	latitudeMachine.Status.Conditions = append(conditions, condition)
}

func (r *LatitudeMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudemachine")

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeMachine{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
