/*

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

package clusterexternalsecret

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// newTestScheme builds a runtime scheme with all types used by the tests.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add alibabacloud to scheme: %v", err)
	}
	return scheme
}

// newTestCES builds a ClusterExternalSecret that provisions into the
// namespaces listed in its conditions.
func newTestCES(name string, namespaces ...string) *api.ClusterExternalSecret {
	return &api.ClusterExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Finalizers: []string{clusterExternalSecretFinalizer},
		},
		Spec: api.ClusterExternalSecretSpec{
			Conditions: []api.ClusterExternalSecretCondition{
				{Namespaces: namespaces},
			},
		},
	}
}

// newTestNamespace builds a labeled namespace object.
func newTestNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// newTestReconciler wires the reconciler against the given fake client.
func newTestReconciler(c client.Client, scheme *runtime.Scheme) *ClusterExternalSecretReconciler {
	return &ClusterExternalSecretReconciler{
		Client: c,
		Scheme: scheme,
		Log:    logr.Discard(),
		Ctx:    context.Background(),
	}
}

// getReadyCondition returns the Ready condition of the CES stored in the
// fake client (freshly re-read), or nil when no Ready condition exists.
func getReadyCondition(t *testing.T, c client.Client, cesName string) *api.ClusterExternalSecretStatusCondition {
	t.Helper()
	refreshed := &api.ClusterExternalSecret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: cesName}, refreshed); err != nil {
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	}
	for i := range refreshed.Status.Conditions {
		if refreshed.Status.Conditions[i].Type == ClusterExternalSecretReady {
			return &refreshed.Status.Conditions[i]
		}
	}
	return nil
}

// TestReconcilePartialFailureReadyFalse pins that a partial provisioning
// failure flips Ready to False with a failure summary, while the successful
// namespaces stay in the provisioned ledger.
func TestReconcilePartialFailureReadyFalse(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a", "ns-b")

	// Make the apply of the ExternalSecret fail for ns-b only, and capture
	// the apply of the successful namespace so the distributed object
	// becomes assertable.
	applyErr := fmt.Errorf("simulated apply failure")
	var captured []*api.ExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a"), newTestNamespace("ns-b")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, clnt client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if es, ok := obj.(*api.ExternalSecret); ok {
					if es.Namespace == "ns-b" {
						return applyErr
					}
					// The fake client does not need to support server-side
					// apply: capture the object instead of swallowing it.
					captured = append(captured, es.DeepCopy())
					return nil
				}
				return clnt.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := newTestReconciler(c, scheme)
	r.RotationInterval = 10 * time.Minute
	result, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	})
	if err != nil {
		t.Fatalf("partial failure must not fail the reconcile, got error: %v", err)
	}
	// A partial provisioning failure is still a successful reconcile that
	// requeues with the rotation interval.
	if result.RequeueAfter != 10*time.Minute {
		t.Fatalf("expected requeue after the rotation interval 10m, got %v", result.RequeueAfter)
	}

	// The failed namespace must be recorded and the successful one kept.
	refreshed := &api.ClusterExternalSecret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "test-ces"}, refreshed); err != nil {
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	}
	if len(refreshed.Status.FailedNamespaces) != 1 || refreshed.Status.FailedNamespaces[0].Namespace != "ns-b" {
		t.Fatalf("expected exactly ns-b in FailedNamespaces, got %v", refreshed.Status.FailedNamespaces)
	}
	if len(refreshed.Status.ProvisionedNamespaces) != 1 || refreshed.Status.ProvisionedNamespaces[0] != "ns-a" {
		t.Fatalf("expected exactly ns-a in ProvisionedNamespaces, got %v", refreshed.Status.ProvisionedNamespaces)
	}

	// Ready must be False with a failure summary mentioning ns-b.
	condition := getReadyCondition(t, c, "test-ces")
	if condition == nil {
		t.Fatalf("expected a Ready condition, got none")
	}
	if condition.Status != corev1.ConditionFalse {
		t.Fatalf("expected Ready=False on partial failure, got %s", condition.Status)
	}
	if !strings.Contains(condition.Message, "ns-b") {
		t.Fatalf("expected failure summary to mention ns-b, got %q", condition.Message)
	}

	// The successful namespace must have received the propagated object.
	if len(captured) != 1 {
		t.Fatalf("expected exactly one captured ExternalSecret apply, got %d", len(captured))
	}
	assertExternalSecretContent(t, captured[0], ces, "ns-a")
}

// TestReconcileAllSuccessReadyTrue pins that a fully successful reconcile
// reports Ready=True with an empty failure ledger.
func TestReconcileAllSuccessReadyTrue(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a")

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	r.RotationInterval = time.Hour
	result, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	// The success path must requeue with the rotation interval.
	if result.RequeueAfter != time.Hour || result.Requeue {
		t.Fatalf("expected RequeueAfter=1h Requeue=false, got %+v", result)
	}
	// The core product of the controller is the distributed ExternalSecret:
	// it must carry the CES content verbatim.
	if len(captured) != 1 {
		t.Fatalf("expected exactly one applied ExternalSecret, got %d", len(captured))
	}
	assertExternalSecretContent(t, captured[0].object, ces, "ns-a")

	condition := getReadyCondition(t, c, "test-ces")
	if condition == nil {
		t.Fatalf("expected a Ready condition, got none")
	}
	if condition.Status != corev1.ConditionTrue {
		t.Fatalf("expected Ready=True on full success, got %s", condition.Status)
	}
}

// TestReconcileListFailureKeepsProvisionedLedger pins that a namespace List
// failure keeps the previously provisioned namespaces intact (the finalizer
// cleanup path depends on the ledger) and only records the failure.
func TestReconcileListFailureKeepsProvisionedLedger(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a")
	ces.Status.ProvisionedNamespaces = []string{"ns-a"}

	listErr := fmt.Errorf("simulated list failure")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, clnt client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*corev1.NamespaceList); ok {
					return listErr
				}
				return clnt.List(ctx, list, opts...)
			},
		}).
		Build()

	r := newTestReconciler(c, scheme)
	_, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	})
	if err == nil {
		t.Fatalf("expected reconcile error on namespace list failure, got nil")
	}

	refreshed := &api.ClusterExternalSecret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "test-ces"}, refreshed); err != nil {
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	}

	// The provisioned ledger must survive the List failure.
	if len(refreshed.Status.ProvisionedNamespaces) != 1 || refreshed.Status.ProvisionedNamespaces[0] != "ns-a" {
		t.Fatalf("expected ProvisionedNamespaces to be preserved, got %v", refreshed.Status.ProvisionedNamespaces)
	}
	// The failure must be recorded.
	if len(refreshed.Status.FailedNamespaces) != 1 ||
		!strings.Contains(refreshed.Status.FailedNamespaces[0].Reason, "Failed to list namespaces") {
		t.Fatalf("expected a list-failure entry in FailedNamespaces, got %v", refreshed.Status.FailedNamespaces)
	}
	// Ready must not claim success.
	condition := getReadyCondition(t, c, "test-ces")
	if condition == nil || condition.Status != corev1.ConditionFalse {
		t.Fatalf("expected Ready=False after list failure, got %v", condition)
	}
}

// TestStatusUpdateDebounce pins that an unchanged status does not issue a
// status Update: two identical successful reconciles must result in exactly
// one status write.
func TestStatusUpdateDebounce(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a")

	statusUpdates := 0
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, clnt client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*api.ExternalSecret); ok {
					return nil
				}
				return clnt.Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, clnt client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" {
					statusUpdates++
				}
				return clnt.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	r := newTestReconciler(c, scheme)
	req := ctrlreconcile.Request{NamespacedName: types.NamespacedName{Name: "test-ces"}}

	// First reconcile establishes the Ready=True status -> one write.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if statusUpdates != 1 {
		t.Fatalf("expected exactly 1 status update after the first reconcile, got %d", statusUpdates)
	}

	// Second identical reconcile: the status is unchanged (setCondition
	// preserves LastTransitionTime), so no further write may happen.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if statusUpdates != 1 {
		t.Fatalf("expected the unchanged status to be debounced (still 1 update), got %d", statusUpdates)
	}

	// A status change must be written again.
	fresh := &api.ClusterExternalSecret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "test-ces"}, fresh); err != nil {
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	}
	r.updateStatusWithFailure(logr.Discard(), fresh, []api.ClusterExternalSecretNamespaceFailure{
		{Namespace: "ns-a", Reason: "simulated"},
	})
	if statusUpdates != 2 {
		t.Fatalf("expected a status update when the status actually changed, got %d total", statusUpdates)
	}
}

// TestSetConditionPreservesTransitionTime pins that the LastTransitionTime is
// kept when the condition status does not change (this is what makes the
// DeepEqual debounce work).
func TestSetConditionPreservesTransitionTime(t *testing.T) {
	r := &ClusterExternalSecretReconciler{}
	ces := &api.ClusterExternalSecret{}

	first := api.ClusterExternalSecretStatusCondition{
		Type:   ClusterExternalSecretReady,
		Status: corev1.ConditionTrue,
	}
	r.setCondition(ces, first)
	firstTime := ces.Status.Conditions[0].LastTransitionTime

	// Same status again: transition time must be preserved.
	r.setCondition(ces, api.ClusterExternalSecretStatusCondition{
		Type:   ClusterExternalSecretReady,
		Status: corev1.ConditionTrue,
	})
	if !ces.Status.Conditions[0].LastTransitionTime.Equal(&firstTime) {
		t.Fatalf("expected LastTransitionTime to be preserved for unchanged status, got %v want %v",
			ces.Status.Conditions[0].LastTransitionTime, firstTime)
	}
}

// TestSummarizeNamespaceFailures pins the failure summary format used in the
// Ready condition message.
func TestSummarizeNamespaceFailures(t *testing.T) {
	tests := []struct {
		name     string
		failed   []api.ClusterExternalSecretNamespaceFailure
		contains []string
	}{
		{
			name:     "single failure",
			failed:   []api.ClusterExternalSecretNamespaceFailure{{Namespace: "ns-a", Reason: "boom"}},
			contains: []string{"1 namespace(s)", "ns-a"},
		},
		{
			name:     "cluster-wide failure with empty namespace",
			failed:   []api.ClusterExternalSecretNamespaceFailure{{Namespace: "", Reason: "list failed"}},
			contains: []string{"<cluster-wide>"},
		},
		{
			name: "more than five failures are truncated",
			failed: []api.ClusterExternalSecretNamespaceFailure{
				{Namespace: "ns-1"}, {Namespace: "ns-2"}, {Namespace: "ns-3"},
				{Namespace: "ns-4"}, {Namespace: "ns-5"}, {Namespace: "ns-6"},
			},
			contains: []string{"6 namespace(s)", "ns-5", "..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeNamespaceFailures(tt.failed)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("summary %q does not contain %q", got, want)
				}
			}
		})
	}
}

// appliedExternalSecret is one server-side apply issued by the controller,
// captured by the interceptor instead of being persisted by the fake client.
type appliedExternalSecret struct {
	object *api.ExternalSecret
	patch  client.Patch
}

// captureExternalSecretApplies returns interceptor funcs that record every
// ExternalSecret server-side apply instead of forwarding it to the fake
// client, so the distributed objects become assertable.
func captureExternalSecretApplies(captured *[]appliedExternalSecret) interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, clnt client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if es, ok := obj.(*api.ExternalSecret); ok {
				*captured = append(*captured, appliedExternalSecret{
					object: es.DeepCopy(),
					patch:  patch,
				})
				return nil
			}
			return clnt.Patch(ctx, obj, patch, opts...)
		},
	}
}

// assertExternalSecretContent pins that an applied ExternalSecret carries
// exactly what the ClusterExternalSecret describes: the name derived by
// getExternalSecretName, the propagated metadata, the verbatim spec and a
// controller ownerReference back to the CES.
func assertExternalSecretContent(t *testing.T, es *api.ExternalSecret, ces *api.ClusterExternalSecret, namespace string) {
	t.Helper()

	wantName := getExternalSecretName(ces)
	if es.Name != wantName {
		t.Errorf("expected ExternalSecret name %q (getExternalSecretName rule), got %q", wantName, es.Name)
	}
	if es.Namespace != namespace {
		t.Errorf("expected ExternalSecret in namespace %q, got %q", namespace, es.Namespace)
	}
	if !reflect.DeepEqual(es.Spec, ces.Spec.ExternalSecretSpec) {
		t.Errorf("applied spec does not match ces.Spec.ExternalSecretSpec:\n got %+v\nwant %+v",
			es.Spec, ces.Spec.ExternalSecretSpec)
	}
	if !reflect.DeepEqual(es.Labels, ces.Spec.ExternalSecretMetadata.Labels) {
		t.Errorf("labels not propagated: got %v, want %v", es.Labels, ces.Spec.ExternalSecretMetadata.Labels)
	}
	if !reflect.DeepEqual(es.Annotations, ces.Spec.ExternalSecretMetadata.Annotations) {
		t.Errorf("annotations not propagated: got %v, want %v", es.Annotations, ces.Spec.ExternalSecretMetadata.Annotations)
	}
	if len(es.OwnerReferences) != 1 {
		t.Fatalf("expected exactly one ownerReference on the applied ExternalSecret, got %v", es.OwnerReferences)
	}
	owner := es.OwnerReferences[0]
	if owner.Kind != "ClusterExternalSecret" || owner.Name != ces.Name ||
		owner.APIVersion != api.SchemeGroupVersion.String() {
		t.Errorf("ownerReference does not point to the CES: %+v", owner)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Errorf("expected a controller ownerReference, got %+v", owner)
	}
}

// newProvisionedExternalSecret builds an ExternalSecret exactly where the
// controller would have provisioned it, used as pre-existing state.
func newProvisionedExternalSecret(ces *api.ClusterExternalSecret, namespace string) *api.ExternalSecret {
	return &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      getExternalSecretName(ces),
			Namespace: namespace,
		},
	}
}

// externalSecretExists reports whether the ExternalSecret the controller
// names for the CES exists in the namespace.
func externalSecretExists(t *testing.T, c client.Client, ces *api.ClusterExternalSecret, namespace string) bool {
	t.Helper()
	es := &api.ExternalSecret{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name:      getExternalSecretName(ces),
		Namespace: namespace,
	}, es)
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("failed to look up ExternalSecret in namespace %s: %v", namespace, err)
	return false
}

// getRefreshedCES re-reads the ClusterExternalSecret from the fake client.
func getRefreshedCES(t *testing.T, c client.Client, cesName string) *api.ClusterExternalSecret {
	t.Helper()
	refreshed := &api.ClusterExternalSecret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: cesName}, refreshed); err != nil {
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	}
	return refreshed
}

// TestReconcileAppliesExpectedExternalSecret pins the exact content the
// controller distributes into matching namespaces: the naming rule
// (getExternalSecretName), the propagated labels/annotations, the verbatim
// ExternalSecretSpec, the CES ownerReference and the server-side apply type.
func TestReconcileAppliesExpectedExternalSecret(t *testing.T) {
	tests := []struct {
		name               string
		externalSecretName string
		wantName           string
	}{
		{
			name:               "name defaults to the CES name",
			externalSecretName: "",
			wantName:           "test-ces",
		},
		{
			name:               "explicit externalSecretName overrides the CES name",
			externalSecretName: "custom-es",
			wantName:           "custom-es",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			ces := newTestCES("test-ces", "ns-a")
			ces.Spec.ExternalSecretName = tt.externalSecretName
			ces.Spec.ExternalSecretMetadata = api.ExternalSecretMetadata{
				Labels:      map[string]string{"managed-by": "cluster-external-secret"},
				Annotations: map[string]string{"ack.alibabacloud.com/propagated": "true"},
			}
			ces.Spec.ExternalSecretSpec = api.ExternalSecretSpec{
				Provider: "kms",
				Data: []api.DataSource{
					{Key: "secret-key", Name: "target-key"},
				},
			}

			var captured []appliedExternalSecret
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(ces, newTestNamespace("ns-a")).
				WithStatusSubresource(&api.ClusterExternalSecret{}).
				WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
				Build()

			r := newTestReconciler(c, scheme)
			if _, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-ces"},
			}); err != nil {
				t.Fatalf("unexpected reconcile error: %v", err)
			}

			if len(captured) != 1 {
				t.Fatalf("expected exactly one applied ExternalSecret, got %d", len(captured))
			}
			if captured[0].patch != client.Apply {
				t.Fatalf("expected the ExternalSecret to be distributed via server-side apply, got %T", captured[0].patch)
			}
			if captured[0].object.Name != tt.wantName {
				t.Fatalf("expected ExternalSecret name %q, got %q", tt.wantName, captured[0].object.Name)
			}
			assertExternalSecretContent(t, captured[0].object, ces, "ns-a")
		})
	}
}

// TestReconcileResultRequeue pins the Reconcile result contract: the result
// requeues with the effective rotation interval, a spec-level interval
// overrides the controller default, and DisablePolling suppresses any
// scheduled requeue.
func TestReconcileResultRequeue(t *testing.T) {
	tests := []struct {
		name             string
		reconcilerPeriod time.Duration
		specInterval     *metav1.Duration
		disablePolling   bool
		wantRequeueAfter time.Duration
		wantRequeue      bool
	}{
		{
			name:             "requeues with the controller rotation interval",
			reconcilerPeriod: 10 * time.Minute,
			wantRequeueAfter: 10 * time.Minute,
		},
		{
			name:             "spec rotation interval overrides the controller default",
			reconcilerPeriod: 10 * time.Minute,
			specInterval:     &metav1.Duration{Duration: time.Minute},
			wantRequeueAfter: time.Minute,
		},
		{
			name:             "disabled polling schedules no requeue",
			reconcilerPeriod: 10 * time.Minute,
			disablePolling:   true,
			wantRequeueAfter: 0,
			wantRequeue:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			ces := newTestCES("test-ces", "ns-a")
			ces.Spec.RotationInterval = tt.specInterval

			var captured []appliedExternalSecret
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(ces, newTestNamespace("ns-a")).
				WithStatusSubresource(&api.ClusterExternalSecret{}).
				WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
				Build()

			r := newTestReconciler(c, scheme)
			r.RotationInterval = tt.reconcilerPeriod
			r.DisablePolling = tt.disablePolling
			result, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-ces"},
			})
			if err != nil {
				t.Fatalf("unexpected reconcile error: %v", err)
			}
			if result.RequeueAfter != tt.wantRequeueAfter {
				t.Errorf("expected RequeueAfter %v, got %v", tt.wantRequeueAfter, result.RequeueAfter)
			}
			if result.Requeue != tt.wantRequeue {
				t.Errorf("expected Requeue %v, got %v", tt.wantRequeue, result.Requeue)
			}
		})
	}
}

// TestReconcileAddsFinalizer pins that a freshly created ClusterExternalSecret
// without a finalizer gets one during its first reconcile, and that the
// provisioning still happens in the same pass.
func TestReconcileAddsFinalizer(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a")
	ces.Finalizers = nil

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	if _, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	}); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	refreshed := getRefreshedCES(t, c, "test-ces")
	if !utils.Contains(refreshed.Finalizers, clusterExternalSecretFinalizer) {
		t.Fatalf("expected the finalizer %q to be added, got %v", clusterExternalSecretFinalizer, refreshed.Finalizers)
	}
	// Adding the finalizer must not block provisioning in the same pass.
	if len(captured) != 1 {
		t.Fatalf("expected the ExternalSecret to be applied in the same pass, got %d applies", len(captured))
	}
}

// TestReconcileHandleDeletion pins the deletion path: finalization deletes
// every provisioned ExternalSecret, applies nothing new, and then drops the
// finalizer so the resource can be garbage collected.
func TestReconcileHandleDeletion(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a")
	now := metav1.Now()
	ces.DeletionTimestamp = &now
	ces.Status.ProvisionedNamespaces = []string{"ns-a"}

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a"), newProvisionedExternalSecret(ces, "ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	r.RotationInterval = 7 * time.Minute
	result, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if result.RequeueAfter != 7*time.Minute {
		t.Fatalf("expected the deletion path to requeue with the rotation interval, got %v", result.RequeueAfter)
	}

	// The deletion path must not provision anything.
	if len(captured) != 0 {
		t.Fatalf("expected no ExternalSecret applies during deletion, got %d", len(captured))
	}
	// The provisioned ExternalSecret must be cleaned up by the finalizer.
	if externalSecretExists(t, c, ces, "ns-a") {
		t.Fatalf("expected the provisioned ExternalSecret to be deleted during finalization")
	}
	// The finalizer must be removed; the fake client may then delete the
	// object outright, which is equally acceptable.
	refreshed := &api.ClusterExternalSecret{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "test-ces"}, refreshed)
	switch {
	case apierrors.IsNotFound(err):
		// object gone after the last finalizer was removed
	case err != nil:
		t.Fatalf("failed to re-read ClusterExternalSecret: %v", err)
	default:
		if utils.Contains(refreshed.Finalizers, clusterExternalSecretFinalizer) {
			t.Fatalf("expected the finalizer to be removed, got %v", refreshed.Finalizers)
		}
	}
}

// TestReconcileCleanupOrphanedExternalSecrets pins that ExternalSecrets in
// namespaces that fell out of the selector are deleted once the namespace set
// shrinks, while still-matching namespaces keep their objects.
func TestReconcileCleanupOrphanedExternalSecrets(t *testing.T) {
	scheme := newTestScheme(t)
	// The selector now only matches ns-a, but the ledger still records ns-b.
	ces := newTestCES("test-ces", "ns-a")
	ces.Status.ProvisionedNamespaces = []string{"ns-a", "ns-b"}

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			ces,
			newTestNamespace("ns-a"), newTestNamespace("ns-b"),
			newProvisionedExternalSecret(ces, "ns-a"), newProvisionedExternalSecret(ces, "ns-b"),
		).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	if _, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	}); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	// ns-a still matches and is re-applied.
	if len(captured) != 1 || captured[0].object.Namespace != "ns-a" {
		t.Fatalf("expected exactly one apply targeting ns-a, got %d applies", len(captured))
	}
	if !externalSecretExists(t, c, ces, "ns-a") {
		t.Fatalf("expected the ExternalSecret in ns-a to survive the orphan cleanup")
	}
	// ns-b no longer matches: its orphaned ExternalSecret must be deleted.
	if externalSecretExists(t, c, ces, "ns-b") {
		t.Fatalf("expected the orphaned ExternalSecret in ns-b to be deleted")
	}

	refreshed := getRefreshedCES(t, c, "test-ces")
	if len(refreshed.Status.ProvisionedNamespaces) != 1 || refreshed.Status.ProvisionedNamespaces[0] != "ns-a" {
		t.Fatalf("expected the provisioned ledger to shrink to ns-a, got %v", refreshed.Status.ProvisionedNamespaces)
	}
}

// TestReconcileValidateSecretStoreAccessRevoked pins that a namespace losing
// access to the referenced ClusterSecretStore is recorded as failed and its
// previously provisioned ExternalSecret is deleted.
func TestReconcileValidateSecretStoreAccessRevoked(t *testing.T) {
	scheme := newTestScheme(t)
	ces := newTestCES("test-ces", "ns-a", "ns-b")
	ces.Spec.ExternalSecretSpec = api.ExternalSecretSpec{
		Provider: "kms",
		Data: []api.DataSource{{
			SecretStoreRef: &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore"},
			Key:            "secret-key",
			Name:           "target-key",
		}},
	}
	ces.Status.ProvisionedNamespaces = []string{"ns-a", "ns-b"}

	// The ClusterSecretStore only allows ns-a, so ns-b loses access.
	css := &api.ClusterSecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-store"},
		Spec: api.ClusterSecretStoreSpec{
			Conditions: []api.ClusterSecretStoreCondition{
				{Namespaces: []string{"ns-a"}},
			},
		},
	}

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(
			ces, css,
			newTestNamespace("ns-a"), newTestNamespace("ns-b"),
			newProvisionedExternalSecret(ces, "ns-a"), newProvisionedExternalSecret(ces, "ns-b"),
		).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	if _, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	}); err != nil {
		t.Fatalf("access revocation is a per-namespace failure, reconcile must not error: %v", err)
	}

	// Only ns-a keeps being provisioned.
	if len(captured) != 1 || captured[0].object.Namespace != "ns-a" {
		t.Fatalf("expected exactly one apply targeting ns-a, got %d applies", len(captured))
	}
	if !externalSecretExists(t, c, ces, "ns-a") {
		t.Fatalf("expected the ExternalSecret in ns-a to survive")
	}
	// The ExternalSecret in the namespace that lost access must be deleted.
	if externalSecretExists(t, c, ces, "ns-b") {
		t.Fatalf("expected the ExternalSecret in ns-b to be deleted after access was revoked")
	}

	refreshed := getRefreshedCES(t, c, "test-ces")
	if len(refreshed.Status.ProvisionedNamespaces) != 1 || refreshed.Status.ProvisionedNamespaces[0] != "ns-a" {
		t.Fatalf("expected the provisioned ledger to shrink to ns-a, got %v", refreshed.Status.ProvisionedNamespaces)
	}
	if len(refreshed.Status.FailedNamespaces) != 1 || refreshed.Status.FailedNamespaces[0].Namespace != "ns-b" {
		t.Fatalf("expected exactly ns-b in FailedNamespaces, got %v", refreshed.Status.FailedNamespaces)
	}
	if !strings.Contains(refreshed.Status.FailedNamespaces[0].Reason, "not allowed to access ClusterSecretStore cluster-store") {
		t.Fatalf("expected the failure reason to mention the revoked ClusterSecretStore access, got %q", refreshed.Status.FailedNamespaces[0].Reason)
	}
	condition := getReadyCondition(t, c, "test-ces")
	if condition == nil || condition.Status != corev1.ConditionFalse || !strings.Contains(condition.Message, "ns-b") {
		t.Fatalf("expected Ready=False mentioning ns-b, got %v", condition)
	}
}

// TestReconcileNoMatchingNamespaces pins the diagnostic path when selectors
// match nothing: every existing namespace is recorded with a reason, nothing
// is provisioned and the resource reports Ready=False.
func TestReconcileNoMatchingNamespaces(t *testing.T) {
	scheme := newTestScheme(t)
	// The selector names ns-x, which does not exist, so nothing matches.
	ces := newTestCES("test-ces", "ns-x")

	var captured []appliedExternalSecret
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ces, newTestNamespace("ns-a")).
		WithStatusSubresource(&api.ClusterExternalSecret{}).
		WithInterceptorFuncs(captureExternalSecretApplies(&captured)).
		Build()

	r := newTestReconciler(c, scheme)
	if _, err := r.Reconcile(context.Background(), ctrlreconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-ces"},
	}); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	if len(captured) != 0 {
		t.Fatalf("expected no ExternalSecret applies without matching namespaces, got %d", len(captured))
	}

	refreshed := getRefreshedCES(t, c, "test-ces")
	if len(refreshed.Status.ProvisionedNamespaces) != 0 {
		t.Fatalf("expected an empty provisioned ledger, got %v", refreshed.Status.ProvisionedNamespaces)
	}
	// The diagnostic path records why each existing namespace did not match.
	if len(refreshed.Status.FailedNamespaces) != 1 {
		t.Fatalf("expected exactly one failure entry, got %v", refreshed.Status.FailedNamespaces)
	}
	failure := refreshed.Status.FailedNamespaces[0]
	if failure.Namespace != "ns-a" || !strings.Contains(failure.Reason, "does not match provided selectors") {
		t.Fatalf("expected ns-a to be recorded with a selector-mismatch reason, got %+v", failure)
	}
	condition := getReadyCondition(t, c, "test-ces")
	if condition == nil || condition.Status != corev1.ConditionFalse || !strings.Contains(condition.Message, "ns-a") {
		t.Fatalf("expected Ready=False mentioning ns-a, got %v", condition)
	}
}
