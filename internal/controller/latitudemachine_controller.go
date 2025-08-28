package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"

	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// RBAC para nossos CRDs
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudemachines/finalizers,verbs=update
// Eventos
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type LatitudeMachineReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (r *LatitudeMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx).WithValues("latitudemachine", req.NamespacedName)
	log.Info("reconcile start")

	// Carrega o objeto
	lm := &infrav1.LatitudeMachine{}
	if err := r.Get(ctx, req.NamespacedName, lm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Se está deletando, nada a fazer no hello-world
	if !lm.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Se já está pronto, não faz nada
	if lm.Status.Ready {
		return ctrl.Result{}, nil
	}

	// Patch helper para atualizar status de forma segura
	ph, err := patch.NewHelper(lm, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Hello world: define um ProviderID fake e marca Ready
	pid := fmt.Sprintf("latitude:///hello-%s", lm.Name)
	lm.Status.ProviderID = pid        // <- field string
	lm.Status.Ready = true

	// Evento
	if r.recorder != nil {
		r.recorder.Eventf(lm, corev1.EventTypeNormal, "HelloWorld",
			"Assigned fake ProviderID %q and marked Ready", pid)
	}

	// Aplica patch
	if err := ph.Patch(ctx, lm); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconcile done", "providerID", pid)
	return ctrl.Result{}, nil
}

func (r *LatitudeMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudemachine")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeMachine{}).
		Complete(r)
}

