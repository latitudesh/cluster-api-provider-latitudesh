package controllers

import (
	"context"
	"fmt"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	capiutil "sigs.k8s.io/cluster-api/util"
)

func (r *LatitudeMachineReconciler) getBootstrapUserData(
	ctx context.Context,
	lm *infrav1.LatitudeMachine,
) (string, error) {

	log := ctrl.LoggerFrom(ctx)

	ownerMachine, err := capiutil.GetOwnerMachine(ctx, r.Client, lm.ObjectMeta)
	if err != nil {
		return "", fmt.Errorf("get owner Machine: %w", err)
	}
	if ownerMachine == nil {
		log.Info("Owner Machine not definied; requeue")
		return "", nil
	}

	if ownerMachine.Spec.Bootstrap.DataSecretName == nil || *ownerMachine.Spec.Bootstrap.DataSecretName == "" {
		log.Info("Bootstrap data secret not available; requeue",
			"machine", client.ObjectKeyFromObject(ownerMachine))
		return "", nil
	}

	// Read Secret
	sec := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: lm.Namespace,
		Name:      *ownerMachine.Spec.Bootstrap.DataSecretName,
	}
	if err := r.Client.Get(ctx, key, sec); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Bootstrap not found; requeue", "secret", key)
			return "", nil
		}
		return "", fmt.Errorf("get bootstrap Secret %s: %w", key.String(), err)
	}

	b, ok := sec.Data["value"]
	if !ok || len(b) == 0 {
		return "", fmt.Errorf("bootstrap Secret %s without key 'value'", key.String())
	}

	return string(b), nil
}
