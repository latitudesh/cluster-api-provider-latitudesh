package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"

	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=latitudeclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

type LatitudeClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (r *LatitudeClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := crlog.FromContext(ctx).WithValues("latitudecluster", req.NamespacedName)
	log.Info("reconcile start")

	obj := &infrav1.LatitudeCluster{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if obj.Status.Ready {
		return ctrl.Result{}, nil
	}

	ph, err := patch.NewHelper(obj, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	// hello-world: apenas marca Ready (sem endpoint)
	obj.Status.Ready = true
	r.recorder.Eventf(obj, corev1.EventTypeNormal, "HelloWorld", "LatitudeCluster marked Ready")

	if err := ph.Patch(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("reconcile done")
	return ctrl.Result{}, nil
}

func (r *LatitudeClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.recorder = mgr.GetEventRecorderFor("capl-latitudecluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.LatitudeCluster{}).
		Complete(r)
}
