// Copyright © 2025 Alibaba Cloud. All rights reserved.

// get_external_secret_test.go covers getExternalSecret and
// getExternalSecretWithExtract directly: the target-key resolution
// (ResolveTargetKey vs JMESPath alias), the ReplaceKey value rewriting, the
// JMESPath alias empty-value fallback-key branch, and the per-key error /
// succeeded-key bookkeeping. Uses the shared fakeSecretClient/fakeProvider
// pair from helpers_test.go; tests must stay SERIAL (t.Parallel() forbidden).

package externalsecret

import (
	"context"
	"fmt"
	"testing"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// TestGetExternalSecret drives getExternalSecret against the fake provider,
// asserting the merged output keys, the collected error map, and the
// succeeded-key set for the duplicate-key twin exemption.
func TestGetExternalSecret(t *testing.T) {
	const ns = "default"

	tests := []struct {
		name          string
		dataSources   []api.DataSource
		dataByKey     map[string]map[string][]byte
		failByKey     map[string]error
		wantOut       map[string]string
		wantErrKeys   []string
		wantSucceeded []string
	}{
		{
			name:        "non-jmespath resolves the target key from name (falls back to key)",
			dataSources: []api.DataSource{{Key: "k1", Name: "renamed"}},
			dataByKey:   map[string]map[string][]byte{"k1": {"backend-field": []byte("v1")}},
			// Non-JMESPath sources land under ResolveTargetKey (name here).
			wantOut:       map[string]string{"renamed": "v1"},
			wantSucceeded: []string{"k1"},
		},
		{
			name:        "non-jmespath without name falls back to the key",
			dataSources: []api.DataSource{{Key: "k2"}},
			dataByKey:   map[string]map[string][]byte{"k2": {"whatever": []byte("v2")}},
			wantOut:       map[string]string{"k2": "v2"},
			wantSucceeded: []string{"k2"},
		},
		{
			name: "jmespath output keeps the backend alias key",
			dataSources: []api.DataSource{{
				Key:      "k3",
				JMESPath: []api.JMESPathObject{{Path: "a.b", ObjectAlias: "alias3"}},
			}},
			// For JMESPath the backend already keys the map by the alias.
			dataByKey:     map[string]map[string][]byte{"k3": {"alias3": []byte("v3")}},
			wantOut:       map[string]string{"alias3": "v3"},
			wantSucceeded: []string{"k3"},
		},
		{
			name:        "backend failure is recorded per data.Key and yields no output",
			dataSources: []api.DataSource{{Key: "bad"}},
			failByKey:   map[string]error{"bad": fmt.Errorf("backend down")},
			wantOut:     map[string]string{},
			wantErrKeys: []string{"bad"},
		},
		{
			name: "mixed success and failure across entries",
			dataSources: []api.DataSource{
				{Key: "ok", Name: "ok-target"},
				{Key: "fail"},
			},
			dataByKey:     map[string]map[string][]byte{"ok": {"f": []byte("good")}},
			failByKey:     map[string]error{"fail": fmt.Errorf("boom")},
			wantOut:       map[string]string{"ok-target": "good"},
			wantErrKeys:   []string{"fail"},
			wantSucceeded: []string{"ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &fakeSecretClient{dataByKey: tt.dataByKey, failByKey: tt.failByKey}
			r := newTestReconciler(t, false, sc)
			provider := &fakeProvider{sc: sc}

			out, errMap, succeeded := r.getExternalSecret(context.Background(), provider, tt.dataSources, ns)

			assertOutput(t, out, tt.wantOut)
			assertErrKeys(t, errMap, tt.wantErrKeys)
			assertKeySet(t, succeeded, tt.wantSucceeded)
		})
	}
}

// TestGetExternalSecretWithExtract drives getExternalSecretWithExtract,
// covering the ReplaceKey value rewriting and both JMESPath alias branches
// (non-empty alias vs the empty-alias fallback that keeps the backend key).
func TestGetExternalSecretWithExtract(t *testing.T) {
	const ns = "default"

	tests := []struct {
		name          string
		dataProcess   []api.DataProcess
		extractByKey  map[string]map[string][]byte
		failByKey     map[string]error
		wantOut       map[string]string
		wantErrKeys   []string
		wantSucceeded []string
	}{
		{
			name: "ReplaceKey rewrites the value before it is stored",
			dataProcess: []api.DataProcess{{
				Extract:    &api.DataSource{Key: "ek"},
				ReplaceKey: []api.ReplaceRule{{Source: "-", Target: "_"}},
			}},
			extractByKey:  map[string]map[string][]byte{"ek": {"ek": []byte("hello-world-x")}},
			wantOut:       map[string]string{"ek": "hello_world_x"},
			wantSucceeded: []string{"ek"},
		},
		{
			name: "multiple ReplaceKey rules apply in order",
			dataProcess: []api.DataProcess{{
				Extract:    &api.DataSource{Key: "ek2"},
				ReplaceKey: []api.ReplaceRule{{Source: "a", Target: "b"}, {Source: "b", Target: "c"}},
			}},
			// "a" -> "b" then "b" -> "c": the chained rules turn "aa" into "cc".
			extractByKey:  map[string]map[string][]byte{"ek2": {"ek2": []byte("aa")}},
			wantOut:       map[string]string{"ek2": "cc"},
			wantSucceeded: []string{"ek2"},
		},
		{
			name: "JMESPath alias remaps the backend key to the alias",
			dataProcess: []api.DataProcess{{
				Extract: &api.DataSource{
					Key:      "ek3",
					JMESPath: []api.JMESPathObject{{Path: "field", ObjectAlias: "alias3"}},
				},
			}},
			extractByKey:  map[string]map[string][]byte{"ek3": {"field": []byte("v3")}},
			wantOut:       map[string]string{"alias3": "v3"},
			wantSucceeded: []string{"ek3"},
		},
		{
			name: "JMESPath empty alias falls back to the backend key",
			dataProcess: []api.DataProcess{{
				Extract: &api.DataSource{
					Key:      "ek4",
					JMESPath: []api.JMESPathObject{{Path: "field", ObjectAlias: ""}},
				},
			}},
			// The matching path has an empty ObjectAlias -> the backend key is kept.
			extractByKey:  map[string]map[string][]byte{"ek4": {"field": []byte("v4")}},
			wantOut:       map[string]string{"field": "v4"},
			wantSucceeded: []string{"ek4"},
		},
		{
			name:        "nil extract entries are skipped without error",
			dataProcess: []api.DataProcess{{Extract: nil}},
			wantOut:     map[string]string{},
		},
		{
			name: "backend failure is recorded per extract.Key",
			dataProcess: []api.DataProcess{{
				Extract: &api.DataSource{Key: "ekbad"},
			}},
			failByKey:   map[string]error{"ekbad": fmt.Errorf("extract down")},
			wantOut:     map[string]string{},
			wantErrKeys: []string{"ekbad"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &fakeSecretClient{extractByKey: tt.extractByKey, failByKey: tt.failByKey}
			r := newTestReconciler(t, false, sc)
			provider := &fakeProvider{sc: sc}

			out, errMap, succeeded := r.getExternalSecretWithExtract(context.Background(), provider, tt.dataProcess, ns)

			assertOutput(t, out, tt.wantOut)
			assertErrKeys(t, errMap, tt.wantErrKeys)
			assertKeySet(t, succeeded, tt.wantSucceeded)
		})
	}
}

// assertOutput compares a []byte-valued map against the expected string map.
func assertOutput(t *testing.T, got map[string][]byte, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("output size mismatch: got %v, want %v", got, want)
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("missing output key %q, got %v", k, got)
		}
		if string(gv) != v {
			t.Errorf("output key %q: got %q, want %q", k, string(gv), v)
		}
	}
}

// assertErrKeys asserts the error map carries exactly the expected keys.
func assertErrKeys(t *testing.T, got map[string]error, wantKeys []string) {
	t.Helper()
	if len(got) != len(wantKeys) {
		t.Fatalf("error-map size mismatch: got %v, want keys %v", got, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing error for key %q, got %v", k, got)
		}
	}
}

// assertKeySet asserts a set carries exactly the expected keys.
func assertKeySet(t *testing.T, got map[string]struct{}, wantKeys []string) {
	t.Helper()
	if len(got) != len(wantKeys) {
		t.Fatalf("key-set size mismatch: got %v, want keys %v", got, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in set %v", k, got)
		}
	}
}
