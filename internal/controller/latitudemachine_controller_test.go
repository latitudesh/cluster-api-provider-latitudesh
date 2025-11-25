package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	"github.com/latitudesh/cluster-api-provider-latitudesh/internal/latitude"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

func TestLatitudeMachineController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LatitudeMachine Controller Suite")
}

var _ = Describe("LatitudeMachine Controller", func() {
	var (
		ctx          context.Context
		reconciler   *LatitudeMachineReconciler
		k8sClient    client.Client
		scheme       *runtime.Scheme
		mockClient   *mockLatitudeClient
		fakeRecorder *record.FakeRecorder
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(infrav1.AddToScheme(scheme)).To(Succeed())
		Expect(clusterv1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		mockClient = &mockLatitudeClient{
			servers:  make(map[string]*latitude.Server),
			userdata: make(map[string]string),
		}

		fakeRecorder = record.NewFakeRecorder(100)

		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&infrav1.LatitudeMachine{}).
			WithStatusSubresource(&infrav1.LatitudeCluster{}).
			Build()

		reconciler = &LatitudeMachineReconciler{
			Client:         k8sClient,
			Scheme:         scheme,
			recorder:       fakeRecorder,
			LatitudeClient: mockClient,
		}
	})

	Context("When reconciling a LatitudeMachine", func() {
		It("should add finalizer on first reconcile", func() {
			latitudeMachine := createTestLatitudeMachine()
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify finalizer was added
			updatedMachine := &infrav1.LatitudeMachine{}
			Expect(k8sClient.Get(ctx, req.NamespacedName, updatedMachine)).To(Succeed())
			Expect(updatedMachine.Finalizers).To(ContainElement(LatitudeFinalizerName))
		})

		It("should skip reconciliation when paused", func() {
			latitudeMachine := createTestLatitudeMachine()
			latitudeMachine.Annotations = map[string]string{
				"cluster.x-k8s.io/paused": "true",
			}
			latitudeMachine.Finalizers = []string{LatitudeFinalizerName}
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify no server was created
			Expect(mockClient.servers).To(BeEmpty())
		})

		It("should return nil when resource is not found", func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: "default",
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("should skip reconciliation when already ready", func() {
			latitudeMachine := createTestLatitudeMachine()
			latitudeMachine.Finalizers = []string{LatitudeFinalizerName}
			latitudeMachine.Status.Ready = true
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify no new server was created
			Expect(mockClient.servers).To(BeEmpty())
		})
	})

	Context("When creating infrastructure", func() {
		It("should create server with complete cluster setup", func() {
			// Create full cluster hierarchy
			cluster, latitudeCluster, machine, latitudeMachine, bootstrapSecret := createFullClusterSetup()

			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeCluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, machine)).To(Succeed())
			Expect(k8sClient.Create(ctx, bootstrapSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			// First reconcile - add finalizer
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile - should create server
			mockClient.nextServerStatus = "provisioning"
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Verify server was created
			Expect(mockClient.servers).To(HaveLen(1))

			// Third reconcile - server is ready
			for _, server := range mockClient.servers {
				server.Status = "on"
				server.IPAddress = []string{"192.168.1.100"}
			}

			result, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify machine status
			updatedMachine := &infrav1.LatitudeMachine{}
			Expect(k8sClient.Get(ctx, req.NamespacedName, updatedMachine)).To(Succeed())
			Expect(updatedMachine.Status.Ready).To(BeTrue())
			Expect(updatedMachine.Status.ServerID).NotTo(BeEmpty())
			Expect(updatedMachine.Status.ProviderID).To(ContainSubstring("latitude://"))
			Expect(updatedMachine.Status.Addresses).NotTo(BeEmpty())
		})

		It("should requeue when bootstrap data is not ready", func() {
			// Create cluster setup without bootstrap secret
			cluster, latitudeCluster, machine, latitudeMachine, _ := createFullClusterSetup()
			machine.Spec.Bootstrap.DataSecretName = nil // No bootstrap data yet

			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeCluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, machine)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			// Add finalizer first
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile should wait for bootstrap data
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Verify no server was created
			Expect(mockClient.servers).To(BeEmpty())
		})
	})

	Context("When validating machine spec", func() {
		It("should fail validation when required fields are missing", func() {
			// Create full cluster setup but with invalid spec
			cluster, latitudeCluster, machine, latitudeMachine, bootstrapSecret := createFullClusterSetup()
			latitudeMachine.Spec.OperatingSystem = "" // Missing required field
			latitudeMachine.Finalizers = []string{LatitudeFinalizerName}

			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeCluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, machine)).To(Succeed())
			Expect(k8sClient.Create(ctx, bootstrapSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, latitudeMachine)).To(Succeed())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeMachine.Name,
					Namespace: latitudeMachine.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify condition was set
			updatedMachine := &infrav1.LatitudeMachine{}
			Expect(k8sClient.Get(ctx, req.NamespacedName, updatedMachine)).To(Succeed())
			Expect(updatedMachine.Status.Conditions).NotTo(BeEmpty())
			condition := findCondition(updatedMachine.Status.Conditions, infrav1.InstanceReadyCondition)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(infrav1.InstanceProvisionFailedReason))
		})
	})

	Context("When deleting a LatitudeMachine", func() {
		It("should delete server successfully", func() {
			// Test reconcileDelete directly
			latitudeMachine := createTestLatitudeMachine()
			latitudeMachine.Status.ServerID = "test-server-123"

			// Create mock server
			mockClient.servers["test-server-123"] = &latitude.Server{
				ID:     "test-server-123",
				Status: "on",
			}

			result, err := reconciler.reconcileDelete(ctx, latitudeMachine)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			// Verify server was deleted
			Expect(mockClient.servers).To(BeEmpty())
			// Verify finalizer was removed
			Expect(latitudeMachine.Finalizers).NotTo(ContainElement(LatitudeFinalizerName))
		})

		It("should handle deletion when server is already gone", func() {
			latitudeMachine := createTestLatitudeMachine()
			latitudeMachine.Status.ServerID = ""

			result, err := reconciler.reconcileDelete(ctx, latitudeMachine)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("should requeue on delete failure", func() {
			latitudeMachine := createTestLatitudeMachine()
			latitudeMachine.Status.ServerID = "test-server-123"
			latitudeMachine.Finalizers = []string{LatitudeFinalizerName}

			// Set up mock to fail deletion
			mockClient.deleteServerError = fmt.Errorf("API error")

			result, err := reconciler.reconcileDelete(ctx, latitudeMachine)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Verify finalizer was NOT removed due to error
			Expect(latitudeMachine.Finalizers).To(ContainElement(LatitudeFinalizerName))
		})
	})

	Context("Helper functions", func() {
		It("should generate correct hostname", func() {
			latitudeMachine := createTestLatitudeMachine()
			hostname := reconciler.getHostname(latitudeMachine)
			Expect(hostname).To(Equal("default-test-machine"))
		})

		It("should set and update conditions correctly", func() {
			latitudeMachine := createTestLatitudeMachine()

			// Set initial condition
			reconciler.setCondition(
				latitudeMachine,
				infrav1.InstanceReadyCondition,
				metav1.ConditionFalse,
				infrav1.InstanceNotReadyReason,
				"Server is being provisioned",
			)

			Expect(latitudeMachine.Status.Conditions).To(HaveLen(1))
			condition := latitudeMachine.Status.Conditions[0]
			Expect(condition.Type).To(Equal(infrav1.InstanceReadyCondition))
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))

			// Update condition
			reconciler.setCondition(
				latitudeMachine,
				infrav1.InstanceReadyCondition,
				metav1.ConditionTrue,
				"InstanceReady",
				"Instance is ready",
			)

			Expect(latitudeMachine.Status.Conditions).To(HaveLen(1))
			condition = latitudeMachine.Status.Conditions[0]
			Expect(condition.Type).To(Equal(infrav1.InstanceReadyCondition))
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})

// Helper functions

func createTestLatitudeMachine() *infrav1.LatitudeMachine {
	return &infrav1.LatitudeMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
		},
		Spec: infrav1.LatitudeMachineSpec{
			OperatingSystem: "ubuntu_22_04",
			Plan:            "c3-small-x86",
			// SSH keys are now optional and configured via sshKeySecretRef
			SSHKeySecretRef: nil,
		},
	}
}

func createFullClusterSetup() (*clusterv1.Cluster, *infrav1.LatitudeCluster, *clusterv1.Machine, *infrav1.LatitudeMachine, *corev1.Secret) {
	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: &corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
				Kind:       "LatitudeCluster",
				Name:       "test-latitude-cluster",
				Namespace:  "default",
			},
		},
	}

	latitudeCluster := &infrav1.LatitudeCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-latitude-cluster",
			Namespace: "default",
		},
		Spec: infrav1.LatitudeClusterSpec{
			Location: "SAO",
			ProjectRef: &infrav1.ProjectRef{
				ProjectID: "test-project-123",
			},
		},
	}

	secretName := "test-bootstrap-secret"
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: "test-cluster",
			},
		},
		Spec: clusterv1.MachineSpec{
			ClusterName: "test-cluster",
			Bootstrap: clusterv1.Bootstrap{
				DataSecretName: &secretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				APIVersion: "infrastructure.cluster.x-k8s.io/v1beta1",
				Kind:       "LatitudeMachine",
				Name:       "test-machine",
				Namespace:  "default",
			},
		},
	}

	latitudeMachine := &infrav1.LatitudeMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-machine",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "cluster.x-k8s.io/v1beta1",
					Kind:       "Machine",
					Name:       machine.Name,
					UID:        machine.UID,
				},
			},
		},
		Spec: infrav1.LatitudeMachineSpec{
			OperatingSystem: "ubuntu_22_04",
			Plan:            "c3-small-x86",
			// SSH keys are now optional and configured via sshKeySecretRef
			SSHKeySecretRef: nil,
		},
	}

	bootstrapSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"value": []byte("#!/bin/bash\necho 'bootstrap data'"),
		},
	}

	return cluster, latitudeCluster, machine, latitudeMachine, bootstrapSecret
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// Mock Latitude client

type mockLatitudeClient struct {
	servers           map[string]*latitude.Server
	userdata          map[string]string
	nextServerStatus  string
	createServerError error
	getServerError    error
	deleteServerError error
}

func (m *mockLatitudeClient) CreateServer(ctx context.Context, spec latitude.ServerSpec) (*latitude.Server, error) {
	if m.createServerError != nil {
		return nil, m.createServerError
	}

	serverID := fmt.Sprintf("server-%d", len(m.servers)+1)
	status := "provisioning"
	if m.nextServerStatus != "" {
		status = m.nextServerStatus
	}

	server := &latitude.Server{
		ID:       serverID,
		Status:   status,
		Hostname: spec.Hostname,
	}

	m.servers[serverID] = server
	return server, nil
}

func (m *mockLatitudeClient) GetServer(ctx context.Context, serverID string) (*latitude.Server, error) {
	if m.getServerError != nil {
		return nil, m.getServerError
	}

	server, ok := m.servers[serverID]
	if !ok {
		return nil, fmt.Errorf("server not found")
	}

	return server, nil
}

func (m *mockLatitudeClient) DeleteServer(ctx context.Context, serverID string) error {
	if m.deleteServerError != nil {
		return m.deleteServerError
	}

	delete(m.servers, serverID)
	return nil
}

func (m *mockLatitudeClient) CreateUserData(ctx context.Context, req latitude.CreateUserDataRequest) (string, error) {
	udID := fmt.Sprintf("userdata-%d", len(m.userdata)+1)
	m.userdata[udID] = req.Content
	return udID, nil
}

func (m *mockLatitudeClient) GetAvailableRegions(ctx context.Context) ([]string, error) {
	return []string{"SAO", "NYC"}, nil
}

func (m *mockLatitudeClient) GetAvailablePlans(ctx context.Context) ([]string, error) {
	return []string{"c3-small-x86", "c3-medium-x86"}, nil
}

func (m *mockLatitudeClient) DeleteUserData(ctx context.Context, userDataID string) error {
	delete(m.userdata, userDataID)
	return nil
}

func (m *mockLatitudeClient) WaitForServer(ctx context.Context, serverID string, targetStatus string, timeout time.Duration) (*latitude.Server, error) {
	return m.GetServer(ctx, serverID)
}

func (m *mockLatitudeClient) CreateVLAN(ctx context.Context, req latitude.CreateVLANRequest) (*latitude.VLAN, error) {
	return &latitude.VLAN{
		ID:      "mock-vlan-id",
		VID:     100,
		Subnet:  req.Subnet,
		Project: req.ProjectID,
	}, nil
}

func (m *mockLatitudeClient) GetVLAN(ctx context.Context, vlanID string) (*latitude.VLAN, error) {
	return &latitude.VLAN{
		ID:      vlanID,
		VID:     100,
		Subnet:  "10.8.0.0/24",
		Project: "mock-project-id",
	}, nil
}

func (m *mockLatitudeClient) ListVLANs(ctx context.Context, projectID string) ([]*latitude.VLAN, error) {
	return []*latitude.VLAN{}, nil
}

func (m *mockLatitudeClient) DeleteVLAN(ctx context.Context, vlanID string) error {
	return nil
}

func (m *mockLatitudeClient) AttachServerToVLAN(ctx context.Context, serverID, vlanID string) error {
	return nil
}

func (m *mockLatitudeClient) DetachServerFromVLAN(ctx context.Context, serverID, vlanID string) error {
	return nil
}
