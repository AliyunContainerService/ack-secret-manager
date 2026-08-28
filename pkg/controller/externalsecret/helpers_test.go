// Copyright © 2025 Alibaba Cloud. All rights reserved.

// helpers_test.go holds the shared test fixtures of the externalsecret
// package: the fake SecretClient/Provider pair, the reconciler builders, and
// small lookup utilities.
//
// NOTE: the reconciler builders mutate the process-global provider registry;
// tests must stay SERIAL (t.Parallel() forbidden) and each builder restores
// the previous registry entry via t.Cleanup.

package externalsecret

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// serialTestGuard fails fast if two tests enter the shared reconciler setup
// concurrently. The builders mutate the process-global provider registry, so
// the package MUST run serially (t.Parallel() forbidden). A package-level
// TryLock turns an accidental t.Parallel() into an immediate, clearly worded
// failure instead of a flaky registry race.
var serialTestGuard sync.Mutex

// guardSerialTest acquires the package-wide serial guard for the duration of
// the calling test and releases it on cleanup. A failed TryLock means another
// test is already inside the shared setup -- i.e. something ran in parallel.
func guardSerialTest(t *testing.T) {
	t.Helper()
	if !serialTestGuard.TryLock() {
		t.Fatalf("externalsecret tests must run serially: concurrent reconciler setup detected " +
			"(the shared provider registry is process-global; do NOT call t.Parallel() in this package)")
	}
	t.Cleanup(serialTestGuard.Unlock)
}

// fakeSecretClient is a backend.SecretClient whose responses are keyed by
// data.Key / extract.Key, letting each spec entry succeed or fail
// independently. failByName (checked first) keys failures by data.Name so
// duplicate data.Key entries with different names can take mixed outcomes.
type fakeSecretClient struct {
	dataByKey    map[string]map[string][]byte // spec.data success payloads by data.Key
	extractByKey map[string]map[string][]byte // dataProcess success payloads by extract.Key
	failByKey    map[string]error             // failures by data.Key / extract.Key
	failByName   map[string]error             // optional: failures by data.Name (finer-grained than failByKey)
}

func (c *fakeSecretClient) GetName() string { return "fake-backend" }

func (c *fakeSecretClient) GetExternalSecret(_ context.Context, data *api.DataSource, _ client.Client) (map[string][]byte, error) {
	if err, failed := c.failByName[data.Name]; failed {
		return nil, err
	}
	if err, failed := c.failByKey[data.Key]; failed {
		return nil, err
	}
	return c.dataByKey[data.Key], nil
}

func (c *fakeSecretClient) GetExternalSecretWithExtract(_ context.Context, data *api.DataProcess, _ client.Client) (map[string][]byte, error) {
	if data.Extract == nil {
		return nil, fmt.Errorf("nil extract")
	}
	if err, failed := c.failByKey[data.Extract.Key]; failed {
		return nil, err
	}
	return c.extractByKey[data.Extract.Key], nil
}

// fakeProvider satisfies backend.Provider and always serves the configured
// fake SecretClient (no caching / store plumbing needed for these tests).
type fakeProvider struct {
	sc backend.SecretClient
}

func (p *fakeProvider) Register(string, backend.SecretClient) {}
func (p *fakeProvider) GetClient(string) (backend.SecretClient, error) {
	if p.sc == nil {
		return nil, fmt.Errorf("no fake client configured")
	}
	return p.sc, nil
}
func (p *fakeProvider) Delete(string)         {}
func (p *fakeProvider) DeletePrefixed(string) {}
func (p *fakeProvider) NewClient(context.Context, *api.SecretStore, client.Client, string) (backend.SecretClient, error) {
	return p.sc, nil
}
func (p *fakeProvider) NewClientByENV(string) (backend.SecretClient, error) { return p.sc, nil }
func (p *fakeProvider) GetName() string                                     { return backend.ProviderKMSName }
func (p *fakeProvider) GetRegion() string                                   { return "" }
func (p *fakeProvider) GetEndpoint() string                                 { return "" }
func (p *fakeProvider) GetClusterId() string                                { return "" }
func (p *fakeProvider) GetUid() string                                      { return "" }

// newTestReconciler builds a reconciler on a fake client seeded with the
// given objects and registers a fake "kms" provider; t.Cleanup restores the
// previous global registry entry. Mutates the process-global provider
// registry: tests must stay SERIAL.
func newTestReconciler(t *testing.T, cleanup bool, sc *fakeSecretClient, objs ...client.Object) *ExternalSecretReconciler {
	t.Helper()
	guardSerialTest(t)
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&api.ExternalSecret{}).
		WithIndex(&api.ExternalSecret{}, storeRefIndexField, storeRefIndexKeys).
		WithObjects(objs...).
		Build()
	prev := backend.GetProviderByName(backend.ProviderKMSName)
	backend.RegisterProvider(backend.ProviderKMSName, &fakeProvider{sc: sc})
	t.Cleanup(func() {
		if prev != nil {
			backend.RegisterProvider(backend.ProviderKMSName, prev)
		} else {
			backend.DeleteProvider(backend.ProviderKMSName)
		}
	})
	return &ExternalSecretReconciler{
		Client:                 cl,
		APIReader:              cl,
		Log:                    logr.Discard(),
		Ctx:                    context.Background(),
		CleanUpSecretOnFailure: cleanup,
		KmsLimiter:             ProviderLimiter{SecretPullLimiter: rate.NewLimiter(1000, 100)},
		OosLimiter:             ProviderLimiter{SecretPullLimiter: rate.NewLimiter(1000, 100)},
		// Production default (--process-cluster-secret-store=true); tests for
		// the degraded mode flip this explicitly.
		ProcessClusterSecretStore: true,
	}
}

// getTestSecret fetches the target Secret from the reconciler's fake client,
// returning nil when the Secret does not exist.
func getTestSecret(t *testing.T, r *ExternalSecretReconciler, ns, name string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	err := r.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, secret)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("get secret %s/%s: %v", ns, name, err)
	}
	return secret
}

// statusHasKey reports whether the ExternalSecret status carries a
// dataSyncResults entry for the given key.
func statusHasKey(es *api.ExternalSecret, key string) bool {
	for _, res := range es.Status.DataSyncResults {
		if res.ExternalSecretKey == key {
			return true
		}
	}
	return false
}

// cleanupRecordingProvider is a backend.Provider that records every Delete
// call so tests can assert exactly which cache keys were deregistered.
type cleanupRecordingProvider struct {
	deleted []string
}

func (p *cleanupRecordingProvider) Register(string, backend.SecretClient) {}
func (p *cleanupRecordingProvider) GetClient(string) (backend.SecretClient, error) {
	return nil, fmt.Errorf("not needed in cleanup tests")
}
func (p *cleanupRecordingProvider) Delete(clientKey string) {
	p.deleted = append(p.deleted, clientKey)
}
func (p *cleanupRecordingProvider) DeletePrefixed(clientKey string) {
	p.deleted = append(p.deleted, clientKey)
}
func (p *cleanupRecordingProvider) NewClient(context.Context, *api.SecretStore, client.Client, string) (backend.SecretClient, error) {
	return nil, nil
}
func (p *cleanupRecordingProvider) NewClientByENV(string) (backend.SecretClient, error) {
	return nil, nil
}
func (p *cleanupRecordingProvider) GetName() string      { return backend.ProviderKMSName }
func (p *cleanupRecordingProvider) GetRegion() string    { return "" }
func (p *cleanupRecordingProvider) GetEndpoint() string  { return "" }
func (p *cleanupRecordingProvider) GetClusterId() string { return "" }
func (p *cleanupRecordingProvider) GetUid() string       { return "" }

// newCleanupReconciler builds a reconciler on a fake client seeded with the
// given objects and registers the recording provider as "kms"; t.Cleanup
// restores the previous global registry entry (or removes the slot when it
// was empty). Mutates the process-global provider registry: tests must stay
// SERIAL and never call t.Parallel().
func newCleanupReconciler(t *testing.T, provider *cleanupRecordingProvider, objs ...client.Object) *ExternalSecretReconciler {
	t.Helper()
	guardSerialTest(t)
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&api.ExternalSecret{}).
		WithObjects(objs...).Build()
	prev := backend.GetProviderByName(backend.ProviderKMSName)
	backend.RegisterProvider(backend.ProviderKMSName, provider)
	t.Cleanup(func() {
		if prev != nil {
			backend.RegisterProvider(backend.ProviderKMSName, prev)
		} else {
			backend.DeleteProvider(backend.ProviderKMSName)
		}
	})
	return &ExternalSecretReconciler{
		Client:    cl,
		APIReader: cl,
		Log:       logr.Discard(),
		Ctx:       context.Background(),
	}
}

// endpointES builds an ExternalSecret whose single spec.data entry references
// the given store ref (nil = ENV auth) with the given custom endpoint.
func endpointES(name, uid string, storeRef *api.SecretStoreRef, endpoint string) *api.ExternalSecret {
	return &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(uid),
		},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "backend-key", KmsEndpoint: endpoint, SecretStoreRef: storeRef},
				// A plain default-endpoint entry alongside: its client is owned
				// by the SecretStore controller and must NEVER be deleted here.
				{Key: "plain-key"},
			},
		},
	}
}

// strPtr returns a pointer to the given string (test helper).
func strPtr(s string) *string {
	return &s
}

// buildTestExternalSecret builds a minimal ExternalSecret carrying the given
// template spec for ProcessAllTemplates-based tests.
func buildTestExternalSecret(template *api.ExternalSecretTemplate) *api.ExternalSecret {
	return &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "template-test-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Target: &api.ExternalSecretTarget{
				Name:     "template-test-secret",
				Template: template,
			},
		},
	}
}
