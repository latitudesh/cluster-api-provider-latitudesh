package controller_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	controllers "github.com/latitudesh/cluster-api-provider-latitudesh/internal/controller"
	"github.com/latitudesh/cluster-api-provider-latitudesh/test/mocks/latitude"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("LatitudeClusterReconciler", func() {
	var (
		reconciler      *controllers.LatitudeClusterReconciler
		ctx             context.Context
		latitudeCluster *infrav1.LatitudeCluster
		cluster         *clusterv1.Cluster
		s               *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		s = runtime.NewScheme()
		_ = scheme.AddToScheme(s)
		_ = infrav1.AddToScheme(s)
		_ = clusterv1.AddToScheme(s)

		// Create test cluster
		cluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create test LatitudeCluster
		latitudeCluster = &infrav1.LatitudeCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-latitude-cluster",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: clusterv1.GroupVersion.String(),
						Kind:       "Cluster",
						Name:       cluster.Name,
						UID:        cluster.UID,
					},
				},
			},
			Spec: infrav1.LatitudeClusterSpec{
				Location: "SAO",
				ProjectRef: &infrav1.ProjectRef{
					ProjectID: "test-project-id",
				},
			},
		}

		// Create fake client
		fakeClient := fake.NewClientBuilder().
			WithScheme(s).
			WithStatusSubresource(&infrav1.LatitudeCluster{}).
			Build()

		// Create mock Latitude client
		mockLatitudeClient := &latitude.MockClient{
			GetAvailableRegionsFunc: func(ctx context.Context) ([]string, error) {
				return []string{"SAO", "NYC", "LON"}, nil
			},
		}

		reconciler = &controllers.LatitudeClusterReconciler{
			Client:         fakeClient,
			Scheme:         s,
			LatitudeClient: mockLatitudeClient,
		}
		reconciler.SetRecorder(record.NewFakeRecorder(100))
	})

	Context("When reconciling a new LatitudeCluster", func() {
		It("should add a finalizer", func() {
			Expect(reconciler.Client.Create(ctx, cluster)).To(Succeed())
			Expect(reconciler.Client.Create(ctx, latitudeCluster)).To(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeCluster.Name,
					Namespace: latitudeCluster.Namespace,
				},
			}

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated object
			updated := &infrav1.LatitudeCluster{}
			err = reconciler.Client.Get(ctx, req.NamespacedName, updated)
			Expect(err).NotTo(HaveOccurred())

			// Verify finalizer was added
			Expect(updated.Finalizers).To(ContainElement(controllers.LatitudeClusterFinalizerName))
		})

		It("should validate cluster spec and reject invalid location", func() {
			// Create cluster with invalid spec
			invalidCluster := latitudeCluster.DeepCopy()
			invalidCluster.Spec.Location = ""

			Expect(reconciler.Client.Create(ctx, cluster)).To(Succeed())
			Expect(reconciler.Client.Create(ctx, invalidCluster)).To(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      invalidCluster.Name,
					Namespace: invalidCluster.Namespace,
				},
			}

			// First reconcile: adds finalizer
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile: validates spec and sets condition
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Now verify the condition was set
			updated := &infrav1.LatitudeCluster{}
			Expect(reconciler.Client.Get(ctx, req.NamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Conditions).To(HaveLen(1))
			Expect(updated.Status.Conditions[0].Reason).To(Equal(controllers.InvalidClusterConfigReason))
		})

		It("should validate cluster spec and reject missing projectRef", func() {
			invalidCluster := latitudeCluster.DeepCopy()
			invalidCluster.Spec.ProjectRef = nil

			Expect(reconciler.Client.Create(ctx, cluster)).To(Succeed())
			Expect(reconciler.Client.Create(ctx, invalidCluster)).To(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      invalidCluster.Name,
					Namespace: invalidCluster.Namespace,
				},
			}

			// First reconcile: adds finalizer
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile: validates spec and sets condition
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Now verify the condition was set
			updated := &infrav1.LatitudeCluster{}
			Expect(reconciler.Client.Get(ctx, req.NamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Conditions).To(HaveLen(1))
			Expect(updated.Status.Conditions[0].Reason).To(Equal(controllers.InvalidClusterConfigReason))
		})
	})

	Context("When deleting a LatitudeCluster", func() {
		It("should remove the finalizer after cleanup", func() {
			// Create objects WITHOUT DeletionTimestamp first
			clusterForDeletion := cluster.DeepCopy()
			latitudeForDeletion := latitudeCluster.DeepCopy()
			latitudeForDeletion.Finalizers = []string{controllers.LatitudeClusterFinalizerName}

			Expect(reconciler.Client.Create(ctx, clusterForDeletion)).To(Succeed())
			Expect(reconciler.Client.Create(ctx, latitudeForDeletion)).To(Succeed())

			// Now mark for deletion using Delete
			Expect(reconciler.Client.Delete(ctx, latitudeForDeletion)).To(Succeed())

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      latitudeForDeletion.Name,
					Namespace: latitudeForDeletion.Namespace,
				},
			}

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify object was fully deleted (not found)
			updated := &infrav1.LatitudeCluster{}
			err = reconciler.Client.Get(ctx, req.NamespacedName, updated)
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
