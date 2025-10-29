package controllers

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
)

// RBAC:
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachinetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachinetemplates/status,verbs=get
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachinetemplates/finalizers,verbs=update

type LatitudeMachineTemplateReconciler struct {
	client.Client
}

func (r *LatitudeMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithValues("latitudemachinetemplate", req.NamespacedName)
	l.Info("reconcile start/end (noop)")
	return ctrl.Result{}, nil
}

func (r *LatitudeMachineTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeMachineTemplate{}).
		Complete(r)
}
