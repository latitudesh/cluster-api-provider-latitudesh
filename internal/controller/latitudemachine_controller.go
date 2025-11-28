package controllers

import (
	"context"
	"encoding/base64"
	"errors"
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
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/scope"

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
	LatitudeClient latitude.ClientInterface
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
	if !latitudeMachine.DeletionTimestamp.IsZero() {
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

	// If already failed permanently, don't retry
	if latitudeMachine.Status.FailureReason != nil {
		log.Info("Machine has permanent failure, not retrying")
		return ctrl.Result{}, nil
	}

	// Get owner machine
	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, latitudeMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get owner machine: %w", err)
	}
	if ownerMachine == nil {
		log.Info("Owner machine not found yet, waiting")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Create machine scope
	machineScope, err := scope.NewMachineScope(scope.MachineScopeParams{
		Client:          r.Client,
		Logger:          log,
		Machine:         ownerMachine,
		LatitudeMachine: latitudeMachine,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create machine scope: %w", err)
	}

	// Check if this is a worker node and if control plane has failed
	isControlPlane := false
	if ownerMachine.Labels != nil {
		_, isControlPlane = ownerMachine.Labels["cluster.x-k8s.io/control-plane"]
	}

	if !isControlPlane {
		// Check if control plane has failed
		controlPlaneFailed, err := r.checkControlPlaneFailed(ctx, latitudeMachine)
		if err != nil {
			log.Error(err, "failed to check control plane status")
		} else if controlPlaneFailed {
			log.Info("Control plane has failed, marking worker as failed too")
			r.setMachineFailed(latitudeMachine, "Control plane provisioning failed, cannot provision worker nodes")
			r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceProvisionFailedReason, "Control plane failed")
			r.recorder.Eventf(latitudeMachine, corev1.EventTypeWarning, "ControlPlaneFailed", "Cannot provision worker: control plane has failed")
			return ctrl.Result{}, nil
		}
	}

	// Check if we have required fields
	if err := r.validateMachineSpec(ctx, latitudeMachine); err != nil {
		log.Info("Invalid machine spec", "error", err)
		r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceProvisionFailedReason, err.Error())
		return ctrl.Result{}, nil
	}

	// Create or get server from Latitude.sh
	server, err := r.reconcileServer(ctx, machineScope, patchHelper)
	if err != nil {
		log.Error(err, "failed to reconcile server")

		if isPermanentError(err) {
			log.Info("Permanent error detected, marking as failed", "error", err)
			r.setMachineFailed(latitudeMachine, err.Error())
			r.setCondition(latitudeMachine, infrav1.InstanceReadyCondition, metav1.ConditionFalse, infrav1.InstanceProvisionFailedReason, err.Error())
			r.recorder.Eventf(latitudeMachine, corev1.EventTypeWarning, "ProvisioningFailed", "Failed to create server (permanent error): %v", err)
			// Don't return error to avoid automatic requeue
			// Requeue after a longer interval to allow manual intervention
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}
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
	machineScope.SetProviderID(pid)

	// Update machine status
	machineScope.SetReady()
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

//nolint:unparam // error may be used in future
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

func (r *LatitudeMachineReconciler) reconcileServer(ctx context.Context, machineScope *scope.MachineScope, patchHelper *patch.Helper) (*latitude.Server, error) {
	log := crlog.FromContext(ctx)
	latitudeMachine := machineScope.LatitudeMachine

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

	// Get bootstrap data using the new scope method
	userData, err := machineScope.GetBootstrapData(ctx)
	if err != nil {
		if errors.Is(err, scope.ErrBootstrapDataNotReady) {
			// Not an error, just not ready yet
			log.Info("Bootstrap user data not ready yet")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get bootstrap data: %w", err)
	}

	// Inject VLAN configuration into userdata if VLAN is configured
	userData, err = r.injectVLANConfig(ctx, latitudeMachine, userData)
	if err != nil {
		log.Error(err, "Failed to inject VLAN config into userdata")
		// Continue without VLAN config - it can be configured manually
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

	// Get SSH keys from secret
	sshKeys, err := r.getSSHKeys(ctx, latitudeMachine)
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH keys: %w", err)
	}

	// Create new server
	spec := latitude.ServerSpec{
		Project:         r.getProjectID(ctx, latitudeMachine),
		Plan:            latitudeMachine.Spec.Plan,
		OperatingSystem: latitudeMachine.Spec.OperatingSystem,
		Site:            r.getSite(ctx, latitudeMachine),
		Hostname:        r.getHostname(latitudeMachine),
		SSHKeys:         sshKeys,
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

	// Attach server to VLAN if configured
	if err := r.attachServerToVLAN(ctx, machineScope); err != nil {
		log.Error(err, "Failed to attach server to VLAN")
		// Don't fail the reconciliation if VLAN attachment fails
		// The server is still created and can be manually attached later
	}

	return nil, nil
}

func (r *LatitudeMachineReconciler) validateMachineSpec(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) error {
	var errMsgs []string

	if latitudeMachine.Spec.OperatingSystem == "" {
		errMsgs = append(errMsgs, "operatingSystem is required")
	}
	if latitudeMachine.Spec.Plan == "" {
		errMsgs = append(errMsgs, "plan is required")
	}
	if r.getProjectID(ctx, latitudeMachine) == "" {
		errMsgs = append(errMsgs, "projectID is required")
	}
	if r.getSite(ctx, latitudeMachine) == "" {
		errMsgs = append(errMsgs, "site is required")
	}

	if len(errMsgs) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(errMsgs, ", "))
	}
	return nil
}

// checkControlPlaneFailed checks if any control plane machine has failed
func (r *LatitudeMachineReconciler) checkControlPlaneFailed(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) (bool, error) {
	log := crlog.FromContext(ctx)

	// Get the cluster
	cluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil || cluster == nil {
		return false, err
	}

	// List all LatitudeMachines in the same namespace
	machineList := &infrav1.LatitudeMachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(latitudeMachine.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list LatitudeMachines: %w", err)
	}

	// Check each machine to see if it's a control plane machine that failed
	for _, machine := range machineList.Items {
		// Get the owner Machine to check if it's control plane
		ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, machine.ObjectMeta)
		if err != nil || ownerMachine == nil {
			continue
		}

		// Check if this is a control plane machine
		if ownerMachine.Labels != nil {
			if _, isControlPlane := ownerMachine.Labels["cluster.x-k8s.io/control-plane"]; isControlPlane {
				// Check if it has a permanent failure
				if machine.Status.FailureReason != nil {
					log.Info("Control plane machine has failed", "machine", machine.Name)
					return true, nil
				}
			}
		}
	}

	return false, nil
}

func (r *LatitudeMachineReconciler) getProjectID(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) string {
	cluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil {
		return ""
	}
	if cluster != nil && cluster.Spec.ProjectRef != nil {
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

	if cluster == nil {
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
	if err := r.Get(ctx, key, latitudeCluster); err != nil {
		return nil, fmt.Errorf("get LatitudeCluster: %w", err)
	}

	return latitudeCluster, nil
}

// getSSHKeys retrieves SSH key IDs from the referenced secret
func (r *LatitudeMachineReconciler) getSSHKeys(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine) ([]string, error) {
	log := crlog.FromContext(ctx)

	// If no secret reference is provided, return empty array
	if latitudeMachine.Spec.SSHKeySecretRef == nil {
		log.Info("No SSH key secret reference provided, creating server without SSH keys")
		return []string{}, nil
	}

	secretRef := latitudeMachine.Spec.SSHKeySecretRef

	// Get the secret
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}

	// If namespace is not specified in the reference, use the LatitudeMachine's namespace
	if secretKey.Namespace == "" {
		secretKey.Namespace = latitudeMachine.Namespace
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("SSH key secret %s/%s not found", secretKey.Namespace, secretKey.Name)
		}
		return nil, fmt.Errorf("failed to get SSH key secret: %w", err)
	}

	// Get the SSH keys from the secret
	// The secret should contain a key with comma-separated SSH key IDs
	var sshKeyData []byte
	var found bool

	// Try to find the data in the secret
	// First, check if there's a specific key specified in the secret reference
	// Since corev1.SecretReference doesn't have a Key field, we'll use a default key name
	// Users can use any key name, we'll look for common ones
	for _, keyName := range []string{"ssh-key-ids", "sshKeys", "ssh_keys", "keys"} {
		if data, ok := secret.Data[keyName]; ok {
			sshKeyData = data
			found = true
			log.Info("Found SSH keys in secret", "key", keyName)
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("SSH key secret %s/%s does not contain any of the expected keys (ssh-key-ids, sshKeys, ssh_keys, keys)", secretKey.Namespace, secretKey.Name)
	}

	// Parse the comma-separated string
	sshKeyString := strings.TrimSpace(string(sshKeyData))
	if sshKeyString == "" {
		log.Info("SSH key secret is empty, creating server without SSH keys")
		return []string{}, nil
	}

	// Split by comma and trim whitespace
	sshKeys := []string{}
	for _, key := range strings.Split(sshKeyString, ",") {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey != "" {
			sshKeys = append(sshKeys, trimmedKey)
		}
	}

	log.Info("Retrieved SSH keys from secret", "count", len(sshKeys))
	return sshKeys, nil
}

func (r *LatitudeMachineReconciler) getHostname(latitudeMachine *infrav1.LatitudeMachine) string {
	return fmt.Sprintf("%s-%s", latitudeMachine.Namespace, latitudeMachine.Name)
}

//nolint:unparam // conditionType may vary in future
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

// setMachineFailed sets the machine as permanently failed
func (r *LatitudeMachineReconciler) setMachineFailed(latitudeMachine *infrav1.LatitudeMachine, message string) {
	latitudeMachine.Status.Ready = false
	latitudeMachine.Status.FailureReason = stringPtr("CreateError")
	latitudeMachine.Status.FailureMessage = stringPtr(message)
}

// stringPtr returns a pointer to the string value passed in
func stringPtr(s string) *string {
	return &s
}

// isPermanentError checks if an error is a permanent failure that shouldn't be retried frequently
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// List of permanent error codes
	permanentErrors := []string{
		"SERVERS_OUT_OF_STOCK",
		"No stock availability",
	}

	for _, permErr := range permanentErrors {
		if strings.Contains(errStr, permErr) {
			return true
		}
	}

	return false
}

// attachServerToVLAN attaches the server to the cluster's VLAN if configured
func (r *LatitudeMachineReconciler) attachServerToVLAN(ctx context.Context, machineScope *scope.MachineScope) error {
	log := crlog.FromContext(ctx)
	latitudeMachine := machineScope.LatitudeMachine

	// Get server ID
	if latitudeMachine.Status.ServerID == "" {
		return fmt.Errorf("serverID not set")
	}

	// Get LatitudeCluster to check for VLAN config
	latitudeCluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil {
		return fmt.Errorf("failed to get LatitudeCluster: %w", err)
	}

	// Skip if no VLAN configured
	if latitudeCluster.Spec.VLANConfig == nil {
		log.V(1).Info("No VLAN configuration specified, skipping VLAN attachment")
		return nil
	}

	// Skip if VLAN not created yet
	if latitudeCluster.Status.VLANID == nil {
		log.Info("VLAN not created yet, skipping attachment")
		return nil
	}

	serverID := latitudeMachine.Status.ServerID
	vlanID := *latitudeCluster.Status.VLANID

	log.Info("Attaching server to VLAN", "serverID", serverID, "vlanID", vlanID)

	err = r.LatitudeClient.AttachServerToVLAN(ctx, serverID, vlanID)
	if err != nil {
		return fmt.Errorf("failed to attach server to VLAN: %w", err)
	}

	log.Info("Server attached to VLAN successfully", "serverID", serverID, "vlanID", vlanID)
	r.recorder.Eventf(latitudeMachine, "Normal", "ServerAttachedToVLAN", "Attached server %s to VLAN %s", serverID, vlanID)

	return nil
}

// injectVLANConfig injects VLAN netplan configuration into the userdata if VLAN is configured
func (r *LatitudeMachineReconciler) injectVLANConfig(ctx context.Context, latitudeMachine *infrav1.LatitudeMachine, userData string) (string, error) {
	log := crlog.FromContext(ctx)

	// Get LatitudeCluster to check for VLAN config
	latitudeCluster, err := r.getLatitudeCluster(ctx, latitudeMachine)
	if err != nil {
		return userData, fmt.Errorf("failed to get LatitudeCluster: %w", err)
	}

	// Skip if no VLAN configured
	if latitudeCluster.Spec.VLANConfig == nil {
		log.V(1).Info("No VLAN configuration specified, skipping VLAN injection")
		return userData, nil
	}

	// Skip if VID not available yet
	if latitudeCluster.Status.VLANVID == nil {
		log.Info("VLAN VID not available yet, skipping VLAN injection")
		return userData, nil
	}

	// Calculate machine index for IP assignment
	// We use a hash of the machine name to get a consistent index
	machineIndex := getMachineIndex(latitudeMachine.Name)

	// Calculate IP address for this machine
	subnet := latitudeCluster.Spec.VLANConfig.Subnet
	ipAddress, err := CalculateVLANIPAddress(subnet, machineIndex)
	if err != nil {
		return userData, fmt.Errorf("failed to calculate VLAN IP address: %w", err)
	}

	vid := *latitudeCluster.Status.VLANVID
	log.Info("Injecting VLAN configuration into userdata",
		"vid", vid,
		"subnet", subnet,
		"ipAddress", ipAddress,
		"machineIndex", machineIndex)

	// Create VLAN config
	// Use NetworkInterface from config, or default to eno2 (common on Latitude servers)
	networkInterface := latitudeCluster.Spec.VLANConfig.NetworkInterface
	if networkInterface == "" {
		networkInterface = "eno2"
	}

	cfg := VLANNetplanConfig{
		VID:       vid,
		Subnet:    subnet,
		IPAddress: ipAddress,
		Interface: networkInterface,
	}

	// Inject into userdata
	modifiedUserData := InjectVLANConfigIntoCloudInit(userData, cfg)

	log.Info("VLAN configuration injected into userdata successfully")
	return modifiedUserData, nil
}

// getMachineIndex returns a consistent index for a machine based on its name
// This is used to assign unique VLAN IPs to each machine
func getMachineIndex(machineName string) int {
	// Extract numeric suffix from machine name if present
	// e.g., "cluster-control-plane-926v4" -> extract some unique number
	// For simplicity, we'll use a hash-based approach
	hash := 0
	for _, c := range machineName {
		hash = hash*31 + int(c)
	}
	// Return a positive number in range 0-240 (to fit in .10-.250 range)
	index := hash % 241
	if index < 0 {
		index = -index
	}
	return index
}
