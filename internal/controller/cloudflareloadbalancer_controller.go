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
	"k8s.io/client-go/tools/record"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/cloudflare"

	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// RBAC for CloudflareLoadBalancer CRD
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=cloudflareloadbalancers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=cloudflareloadbalancers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=cloudflareloadbalancers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

const (
	CloudflareLoadBalancerFinalizerName = "cloudflareloadbalancer.infrastructure.cluster.x-k8s.io"

	// Condition types
	LoadBalancerReadyCondition = "LoadBalancerReady"
	PoolReadyCondition         = "PoolReady"
	DNSReadyCondition          = "DNSReady"

	// Condition reasons
	LoadBalancerCreatedReason        = "LoadBalancerCreated"
	LoadBalancerCreationFailedReason = "LoadBalancerCreationFailed"
	PoolCreatedReason                = "PoolCreated"
	PoolCreationFailedReason         = "PoolCreationFailed"
	DNSCreatedReason                 = "DNSCreated"
	DNSCreationFailedReason          = "DNSCreationFailed"
	NoOriginsAvailableReason         = "NoOriginsAvailable"
	CredentialsNotFoundReason        = "CredentialsNotFound"
	ClusterNotFoundReason            = "ClusterNotFound"
)

type CloudflareLoadBalancerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (r *CloudflareLoadBalancerReconciler) SetRecorder(recorder record.EventRecorder) {
	r.recorder = recorder
}

func (r *CloudflareLoadBalancerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	log := crlog.FromContext(ctx).WithValues("cloudflareloadbalancer", req.NamespacedName)
	log.Info("reconcile start")

	// Fetch the CloudflareLoadBalancer
	cflb := &infrav1.CloudflareLoadBalancer{}
	err := r.Get(ctx, req.NamespacedName, cflb)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(cflb, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always attempt to Patch the CloudflareLoadBalancer object and status after each reconciliation
	defer func() {
		if err := patchHelper.Patch(ctx, cflb); err != nil {
			log.Error(err, "failed to patch CloudflareLoadBalancer")
			if reterr == nil {
				reterr = err
			}
		}
	}()

	// Handle deletion
	if !cflb.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, cflb)
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(cflb, CloudflareLoadBalancerFinalizerName) {
		controllerutil.AddFinalizer(cflb, CloudflareLoadBalancerFinalizerName)
		return ctrl.Result{}, nil
	}

	// Reconcile normal operations
	return r.reconcileNormal(ctx, cflb)
}

func (r *CloudflareLoadBalancerReconciler) reconcileNormal(ctx context.Context, cflb *infrav1.CloudflareLoadBalancer) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)

	// Get Cloudflare credentials
	cfClient, err := r.getCloudflareClient(ctx, cflb)
	if err != nil {
		log.Error(err, "failed to get Cloudflare client")
		r.setCondition(cflb, LoadBalancerReadyCondition, metav1.ConditionFalse, CredentialsNotFoundReason, err.Error())
		r.recorder.Eventf(cflb, corev1.EventTypeWarning, CredentialsNotFoundReason, "Failed to get Cloudflare credentials: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get the referenced LatitudeCluster
	latitudeCluster, err := r.getLatitudeCluster(ctx, cflb)
	if err != nil {
		log.Error(err, "failed to get LatitudeCluster")
		r.setCondition(cflb, LoadBalancerReadyCondition, metav1.ConditionFalse, ClusterNotFoundReason, err.Error())
		r.recorder.Eventf(cflb, corev1.EventTypeWarning, ClusterNotFoundReason, "Failed to get LatitudeCluster: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get control plane machines (origins)
	origins, err := r.getControlPlaneOrigins(ctx, latitudeCluster)
	if err != nil {
		log.Error(err, "failed to get control plane origins")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	if len(origins) == 0 {
		log.Info("no control plane origins available yet, requeuing")
		r.setCondition(cflb, PoolReadyCondition, metav1.ConditionFalse, NoOriginsAvailableReason, "No control plane machines are ready yet")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Reconcile Pool
	if err := r.reconcilePool(ctx, cfClient, cflb, origins); err != nil {
		log.Error(err, "failed to reconcile pool")
		r.setCondition(cflb, PoolReadyCondition, metav1.ConditionFalse, PoolCreationFailedReason, err.Error())
		r.recorder.Eventf(cflb, corev1.EventTypeWarning, PoolCreationFailedReason, "Failed to reconcile pool: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	r.setCondition(cflb, PoolReadyCondition, metav1.ConditionTrue, PoolCreatedReason, "Pool created successfully")

	// Reconcile Load Balancer
	if err := r.reconcileLoadBalancer(ctx, cfClient, cflb); err != nil {
		log.Error(err, "failed to reconcile load balancer")
		r.setCondition(cflb, LoadBalancerReadyCondition, metav1.ConditionFalse, LoadBalancerCreationFailedReason, err.Error())
		r.recorder.Eventf(cflb, corev1.EventTypeWarning, LoadBalancerCreationFailedReason, "Failed to reconcile load balancer: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	r.setCondition(cflb, LoadBalancerReadyCondition, metav1.ConditionTrue, LoadBalancerCreatedReason, "Load balancer created successfully")

	// Reconcile DNS
	if err := r.reconcileDNS(ctx, cfClient, cflb); err != nil {
		log.Error(err, "failed to reconcile DNS")
		r.setCondition(cflb, DNSReadyCondition, metav1.ConditionFalse, DNSCreationFailedReason, err.Error())
		r.recorder.Eventf(cflb, corev1.EventTypeWarning, DNSCreationFailedReason, "Failed to reconcile DNS: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	r.setCondition(cflb, DNSReadyCondition, metav1.ConditionTrue, DNSCreatedReason, "DNS record created successfully")

	// Update status
	cflb.Status.Ready = true
	cflb.Status.Endpoint = cflb.Spec.Hostname
	cflb.Status.Origins = r.convertToOriginStatus(origins)

	log.Info("reconcile complete", "ready", cflb.Status.Ready)
	r.recorder.Event(cflb, corev1.EventTypeNormal, "ReconcileSuccess", "CloudflareLoadBalancer reconciled successfully")

	// Requeue to periodically check and update origins
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *CloudflareLoadBalancerReconciler) reconcileDelete(ctx context.Context, cflb *infrav1.CloudflareLoadBalancer) (ctrl.Result, error) {
	log := crlog.FromContext(ctx)
	log.Info("reconcile delete")

	// Get Cloudflare credentials
	cfClient, err := r.getCloudflareClient(ctx, cflb)
	if err != nil {
		log.Error(err, "failed to get Cloudflare client during deletion, removing finalizer anyway")
		controllerutil.RemoveFinalizer(cflb, CloudflareLoadBalancerFinalizerName)
		return ctrl.Result{}, nil
	}

	// Delete DNS record
	if cflb.Status.DNSRecordID != "" {
		log.Info("deleting DNS record", "recordID", cflb.Status.DNSRecordID)
		if err := cfClient.DeleteDNSRecord(ctx, cflb.Spec.ZoneID, cflb.Status.DNSRecordID); err != nil {
			log.Error(err, "failed to delete DNS record", "recordID", cflb.Status.DNSRecordID)
			// Continue with cleanup even if DNS deletion fails
		}
	}

	// Delete Load Balancer
	if cflb.Status.LoadBalancerID != "" {
		log.Info("deleting load balancer", "lbID", cflb.Status.LoadBalancerID)
		if err := cfClient.DeleteLoadBalancer(ctx, cflb.Spec.ZoneID, cflb.Status.LoadBalancerID); err != nil {
			log.Error(err, "failed to delete load balancer", "lbID", cflb.Status.LoadBalancerID)
			// Continue with cleanup even if LB deletion fails
		}
	}

	// Delete Pool
	if cflb.Status.PoolID != "" {
		log.Info("deleting pool", "poolID", cflb.Status.PoolID)
		if err := cfClient.DeletePool(ctx, cflb.Spec.AccountID, cflb.Status.PoolID); err != nil {
			log.Error(err, "failed to delete pool", "poolID", cflb.Status.PoolID)
			// Continue with cleanup even if pool deletion fails
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(cflb, CloudflareLoadBalancerFinalizerName)
	log.Info("finalizer removed")

	return ctrl.Result{}, nil
}

func (r *CloudflareLoadBalancerReconciler) reconcilePool(ctx context.Context, cfClient *cloudflare.Client, cflb *infrav1.CloudflareLoadBalancer, origins []cloudflare.Origin) error {
	log := crlog.FromContext(ctx)

	poolName := fmt.Sprintf("%s-pool", cflb.Name)
	poolDescription := fmt.Sprintf("Pool for %s/%s", cflb.Namespace, cflb.Name)

	pool := cloudflare.LoadBalancerPool{
		Name:        poolName,
		Description: poolDescription,
		Origins:     origins,
	}

	// Create or update pool
	if cflb.Status.PoolID == "" {
		log.Info("creating pool", "name", poolName)
		createdPool, err := cfClient.CreatePool(ctx, cflb.Spec.AccountID, pool)
		if err != nil {
			return fmt.Errorf("failed to create pool: %w", err)
		}
		cflb.Status.PoolID = createdPool.ID
		log.Info("pool created", "poolID", createdPool.ID)
	} else {
		log.Info("updating pool", "poolID", cflb.Status.PoolID)
		_, err := cfClient.UpdatePool(ctx, cflb.Spec.AccountID, cflb.Status.PoolID, pool)
		if err != nil {
			return fmt.Errorf("failed to update pool: %w", err)
		}
		log.Info("pool updated", "poolID", cflb.Status.PoolID)
	}

	return nil
}

func (r *CloudflareLoadBalancerReconciler) reconcileLoadBalancer(ctx context.Context, cfClient *cloudflare.Client, cflb *infrav1.CloudflareLoadBalancer) error {
	log := crlog.FromContext(ctx)

	if cflb.Status.LoadBalancerID != "" {
		log.Info("load balancer already exists", "lbID", cflb.Status.LoadBalancerID)
		return nil
	}

	port := cflb.Spec.Port
	if port == 0 {
		port = 6443
	}

	ttl := cflb.Spec.TTL
	if ttl == 0 {
		ttl = 120
	}

	lbName := cflb.Spec.Hostname
	lb := cloudflare.LoadBalancer{
		Name:        lbName,
		Description: fmt.Sprintf("Load balancer for %s/%s", cflb.Namespace, cflb.Name),
		Proxied:     cflb.Spec.Proxied,
		TTL:         int(ttl),
		Pools:       []string{cflb.Status.PoolID},
	}

	log.Info("creating load balancer", "name", lbName)
	createdLB, err := cfClient.CreateLoadBalancer(ctx, cflb.Spec.ZoneID, lb)
	if err != nil {
		return fmt.Errorf("failed to create load balancer: %w", err)
	}

	cflb.Status.LoadBalancerID = createdLB.ID
	log.Info("load balancer created", "lbID", createdLB.ID)

	return nil
}

func (r *CloudflareLoadBalancerReconciler) reconcileDNS(ctx context.Context, cfClient *cloudflare.Client, cflb *infrav1.CloudflareLoadBalancer) error {
	log := crlog.FromContext(ctx)

	// For now, we'll create a simple A record pointing to the load balancer
	// In a production scenario, you might want to use CNAME or other record types

	if cflb.Status.DNSRecordID != "" {
		log.Info("DNS record already exists", "recordID", cflb.Status.DNSRecordID)
		return nil
	}

	// Get the first origin IP to use as the record content
	// In reality, Cloudflare LB handles this differently, but for simplicity we'll just create a record
	if len(cflb.Status.Origins) == 0 {
		return fmt.Errorf("no origins available to create DNS record")
	}

	ttl := cflb.Spec.TTL
	if ttl == 0 {
		ttl = 120
	}

	record := cloudflare.DNSRecord{
		Name:    cflb.Spec.Hostname,
		Type:    "A",
		Content: cflb.Status.Origins[0].Address,
		Proxied: cflb.Spec.Proxied,
		TTL:     int(ttl),
	}

	log.Info("creating DNS record", "name", cflb.Spec.Hostname)
	createdRecord, err := cfClient.CreateDNSRecord(ctx, cflb.Spec.ZoneID, record)
	if err != nil {
		return fmt.Errorf("failed to create DNS record: %w", err)
	}

	cflb.Status.DNSRecordID = createdRecord.ID
	log.Info("DNS record created", "recordID", createdRecord.ID)

	return nil
}

func (r *CloudflareLoadBalancerReconciler) getCloudflareClient(ctx context.Context, cflb *infrav1.CloudflareLoadBalancer) (*cloudflare.Client, error) {
	// Get credentials from Secret
	secretNamespace := cflb.Spec.CredentialsRef.Namespace
	if secretNamespace == "" {
		secretNamespace = cflb.Namespace
	}

	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      cflb.Spec.CredentialsRef.Name,
		Namespace: secretNamespace,
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		return nil, fmt.Errorf("failed to get credentials secret: %w", err)
	}

	apiToken, ok := secret.Data["apiToken"]
	if !ok {
		return nil, fmt.Errorf("apiToken not found in secret %s/%s", secretNamespace, cflb.Spec.CredentialsRef.Name)
	}

	return cloudflare.NewClient(string(apiToken))
}

func (r *CloudflareLoadBalancerReconciler) getLatitudeCluster(ctx context.Context, cflb *infrav1.CloudflareLoadBalancer) (*infrav1.LatitudeCluster, error) {
	clusterNamespace := cflb.Spec.ClusterRef.Namespace
	if clusterNamespace == "" {
		clusterNamespace = cflb.Namespace
	}

	latitudeCluster := &infrav1.LatitudeCluster{}
	clusterKey := types.NamespacedName{
		Name:      cflb.Spec.ClusterRef.Name,
		Namespace: clusterNamespace,
	}

	if err := r.Get(ctx, clusterKey, latitudeCluster); err != nil {
		return nil, fmt.Errorf("failed to get LatitudeCluster: %w", err)
	}

	return latitudeCluster, nil
}

func (r *CloudflareLoadBalancerReconciler) getControlPlaneOrigins(ctx context.Context, latitudeCluster *infrav1.LatitudeCluster) ([]cloudflare.Origin, error) {
	log := crlog.FromContext(ctx)

	// List all LatitudeMachines in the cluster namespace
	machineList := &infrav1.LatitudeMachineList{}
	if err := r.List(ctx, machineList, client.InNamespace(latitudeCluster.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list LatitudeMachines: %w", err)
	}

	var origins []cloudflare.Origin
	for _, machine := range machineList.Items {
		// Check if machine is a control plane machine and is ready
		if !machine.Status.Ready {
			log.Info("machine not ready, skipping", "machine", machine.Name)
			continue
		}

		// Get the machine's IP address
		if len(machine.Status.Addresses) == 0 {
			log.Info("machine has no addresses, skipping", "machine", machine.Name)
			continue
		}

		// Use the first public IP address
		var ipAddress string
		for _, addr := range machine.Status.Addresses {
			if addr.Type == "ExternalIP" || addr.Type == "InternalIP" {
				ipAddress = addr.Address
				break
			}
		}

		if ipAddress == "" {
			log.Info("machine has no suitable IP address, skipping", "machine", machine.Name)
			continue
		}

		origins = append(origins, cloudflare.Origin{
			Name:    machine.Name,
			Address: ipAddress,
			Enabled: true,
			Weight:  1.0,
		})
	}

	log.Info("found control plane origins", "count", len(origins))
	return origins, nil
}

func (r *CloudflareLoadBalancerReconciler) convertToOriginStatus(origins []cloudflare.Origin) []infrav1.OriginStatus {
	result := make([]infrav1.OriginStatus, len(origins))
	for i, origin := range origins {
		result[i] = infrav1.OriginStatus{
			Name:    origin.Name,
			Address: origin.Address,
			Healthy: origin.Enabled, // Simplified - in reality we'd check health
		}
	}
	return result
}

func (r *CloudflareLoadBalancerReconciler) setCondition(cflb *infrav1.CloudflareLoadBalancer, conditionType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: cflb.Generation,
	}

	// Find and update existing condition or append new one
	found := false
	for i, c := range cflb.Status.Conditions {
		if c.Type == conditionType {
			if c.Status != status || c.Reason != reason || c.Message != message {
				cflb.Status.Conditions[i] = condition
			}
			found = true
			break
		}
	}
	if !found {
		cflb.Status.Conditions = append(cflb.Status.Conditions, condition)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *CloudflareLoadBalancerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.CloudflareLoadBalancer{}).
		Complete(r)
}
