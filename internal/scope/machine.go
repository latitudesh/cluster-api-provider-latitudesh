package scope

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrBootstrapDataNotReady is returned when the bootstrap data secret is not ready
	ErrBootstrapDataNotReady = errors.New("bootstrap data secret is not ready")
)

// MachineScopeParams defines the input parameters used to create a new MachineScope.
type MachineScopeParams struct {
	Client          client.Client
	Logger          logr.Logger
	Machine         *clusterv1.Machine
	LatitudeMachine *infrav1.LatitudeMachine
}

// MachineScope defines a scope defined around a machine and its cluster.
type MachineScope struct {
	client.Client
	Logger          logr.Logger
	Machine         *clusterv1.Machine
	LatitudeMachine *infrav1.LatitudeMachine
	patchHelper     *patch.Helper
}

// NewMachineScope creates a new MachineScope from the supplied parameters.
func NewMachineScope(params MachineScopeParams) (*MachineScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a MachineScope")
	}
	if params.Machine == nil {
		return nil, errors.New("machine is required when creating a MachineScope")
	}
	if params.LatitudeMachine == nil {
		return nil, errors.New("latitudeMachine is required when creating a MachineScope")
	}

	helper, err := patch.NewHelper(params.LatitudeMachine, params.Client)
	if err != nil {
		return nil, fmt.Errorf("failed to create patch helper: %w", err)
	}

	return &MachineScope{
		Client:          params.Client,
		Logger:          params.Logger,
		Machine:         params.Machine,
		LatitudeMachine: params.LatitudeMachine,
		patchHelper:     helper,
	}, nil
}

// Close closes the current scope persisting the machine configuration and status.
func (m *MachineScope) Close(ctx context.Context) error {
	return m.patchHelper.Patch(ctx, m.LatitudeMachine, patch.WithStatusObservedGeneration{})
}

// Namespace returns the machine namespace.
func (m *MachineScope) Namespace() string {
	return m.LatitudeMachine.Namespace
}

// Name returns the machine name.
func (m *MachineScope) Name() string {
	return m.LatitudeMachine.Name
}

// GetRawBootstrapData returns the bootstrap data from the secret in the Machine's bootstrap.dataSecretName.
func (m *MachineScope) GetRawBootstrapData(ctx context.Context) ([]byte, error) {
	if m.Machine.Spec.Bootstrap.DataSecretName == nil {
		m.Logger.Info("Bootstrap data secret name not set, requeue")
		return nil, ErrBootstrapDataNotReady
	}

	key := types.NamespacedName{
		Namespace: m.Namespace(),
		Name:      *m.Machine.Spec.Bootstrap.DataSecretName,
	}

	secret := &corev1.Secret{}
	if err := m.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			m.Logger.Info("Bootstrap data secret not found yet", "secret", key)
			return nil, ErrBootstrapDataNotReady
		}
		return nil, fmt.Errorf("failed to get bootstrap data secret: %w", err)
	}

	value, ok := secret.Data["value"]
	if !ok {
		return nil, errors.New("error retrieving bootstrap data: secret value key is missing")
	}

	if len(value) == 0 {
		return nil, errors.New("error retrieving bootstrap data: secret value is empty")
	}

	return value, nil
}

// GetBootstrapData returns the bootstrap data from the secret as a string.
func (m *MachineScope) GetBootstrapData(ctx context.Context) (string, error) {
	value, err := m.GetRawBootstrapData(ctx)
	if err != nil {
		return "", err
	}

	return string(value), nil
}

// IsReady returns true if the machine is ready.
func (m *MachineScope) IsReady() bool {
	return m.LatitudeMachine.Status.Ready
}

// SetReady sets the machine ready status.
func (m *MachineScope) SetReady() {
	m.LatitudeMachine.Status.Ready = true
}

// SetNotReady sets the machine as not ready.
func (m *MachineScope) SetNotReady() {
	m.LatitudeMachine.Status.Ready = false
}

// GetProviderID returns the provider ID.
func (m *MachineScope) GetProviderID() string {
	return m.LatitudeMachine.Status.ProviderID
}

// SetProviderID sets the provider ID.
func (m *MachineScope) SetProviderID(providerID string) {
	m.LatitudeMachine.Spec.ProviderID = &providerID
	m.LatitudeMachine.Status.ProviderID = providerID
}
