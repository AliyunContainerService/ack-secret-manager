package common

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

func TestResolveTargetKey(t *testing.T) {
	tests := []struct {
		name string
		data v1alpha1.DataSource
		want string
	}{
		{
			name: "empty name falls back to key",
			data: v1alpha1.DataSource{Key: "kms-secret", Name: ""},
			want: "kms-secret",
		},
		{
			name: "non-empty name is kept unchanged",
			data: v1alpha1.DataSource{Key: "kms-secret", Name: "custom-name"},
			want: "custom-name",
		},
		{
			// Pin down the degenerate behavior: with both name and key empty the
			// resolved key is empty. key is required by the CRD schema, so this only
			// fixes current behavior against regressions.
			name: "empty name and empty key yields empty string",
			data: v1alpha1.DataSource{Key: "", Name: ""},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveTargetKey(&tt.data); got != tt.want {
				t.Fatalf("ResolveTargetKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProcessExternalSecretData_NameFallback(t *testing.T) {
	payload := []byte("secret-value")
	data := &v1alpha1.DataSource{Key: "kms-secret", Name: ""}

	got, err := ProcessExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if _, empty := got[""]; empty {
		t.Fatalf("produced map must not contain an empty key")
	}
	if v, ok := got["kms-secret"]; !ok || !bytes.Equal(v, payload) {
		t.Fatalf("expected fallback key %q with payload, got %v", "kms-secret", got)
	}
}

func TestProcessExternalSecretData_ExplicitName(t *testing.T) {
	payload := []byte("secret-value")
	data := &v1alpha1.DataSource{Key: "kms-secret", Name: "custom-name"}

	got, err := ProcessExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := got["custom-name"]; !ok || !bytes.Equal(v, payload) {
		t.Fatalf("expected key %q with payload, got %v", "custom-name", got)
	}
	if _, has := got["kms-secret"]; has {
		t.Fatalf("key %q must not appear when name is set", "kms-secret")
	}
}

func TestProcessExternalSecretData_JMESPathUnaffected(t *testing.T) {
	// With jmesPath the keys come from ObjectAlias values, so the
	// name/key fallback must not interfere.
	payload := []byte(`{"username":"alice","password":"p@ss"}`)
	data := &v1alpha1.DataSource{
		Key:  "kms-json-secret",
		Name: "",
		JMESPath: []v1alpha1.JMESPathObject{
			{Path: "username", ObjectAlias: "user"},
			{Path: "password", ObjectAlias: "pass"},
		},
	}

	got, err := ProcessExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if string(got["user"]) != "alice" {
		t.Fatalf("expected user=alice, got %v", got)
	}
	if string(got["pass"]) != "p@ss" {
		t.Fatalf("expected pass=p@ss, got %v", got)
	}
	for _, unwanted := range []string{"", "kms-json-secret"} {
		if _, has := got[unwanted]; has {
			t.Fatalf("unexpected key %q in jmesPath result", unwanted)
		}
	}
}

func TestProcessExternalSecretData_JMESPathEmptyResultFallsBackToRawValue(t *testing.T) {
	// jmesPath parsing SUCCEEDS but the path matches nothing, so GetJsonSecrets
	// returns an empty map without an error: processing must fall back to writing
	// the raw payload under the resolved target key instead of an empty entry set.
	payload := []byte(`{"username":"alice"}`)
	data := &v1alpha1.DataSource{
		Key:  "kms-json-secret",
		Name: "",
		JMESPath: []v1alpha1.JMESPathObject{
			{Path: "nonexistent", ObjectAlias: "missing"},
		},
	}

	got, err := ProcessExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if _, empty := got[""]; empty {
		t.Fatalf("produced map must not contain an empty key")
	}
	if v, ok := got["kms-json-secret"]; !ok || !bytes.Equal(v, payload) {
		t.Fatalf("expected fallback key %q with raw payload, got %v", "kms-json-secret", got)
	}
	if _, has := got["missing"]; has {
		t.Fatalf("ObjectAlias key %q must not appear when the jmesPath result is empty", "missing")
	}
}

// --- ProcessExtractedExternalSecretData (extract path + ReplaceKey rules) ---

func TestProcessExtractedExternalSecretData_JSON(t *testing.T) {
	// JSON input stays JSON: nested values are re-serialized as JSON strings.
	payload := []byte(`{"username":"alice","count":2,"nested":{"a":1}}`)
	data := &v1alpha1.DataProcess{Extract: &v1alpha1.DataSource{Key: "kms-json-secret"}}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %v", len(got), got)
	}
	if string(got["username"]) != "alice" {
		t.Fatalf("expected username=alice, got %q", got["username"])
	}
	if string(got["count"]) != "2" {
		t.Fatalf("expected count=2 (JSON serialization), got %q", got["count"])
	}
	if string(got["nested"]) != `{"a":1}` {
		t.Fatalf("expected nested value serialized as JSON, got %q", got["nested"])
	}
}

func TestProcessExtractedExternalSecretData_YAML(t *testing.T) {
	// YAML input stays YAML: non-string values are re-serialized as YAML.
	payload := []byte("username: alice\ncount: 2\n")
	data := &v1alpha1.DataProcess{Extract: &v1alpha1.DataSource{Key: "kms-yaml-secret"}}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if string(got["username"]) != "alice" {
		t.Fatalf("expected username=alice, got %q", got["username"])
	}
	if string(got["count"]) != "2\n" {
		t.Fatalf("expected count serialized as YAML %q, got %q", "2\n", got["count"])
	}
}

func TestProcessExtractedExternalSecretData_ReplaceKeyRules(t *testing.T) {
	// ReplaceKey rules rewrite the extracted keys via regex; multiple rules
	// are applied in order.
	payload := []byte(`{"prefix_username":"alice","prefix_password":"p@ss"}`)
	data := &v1alpha1.DataProcess{
		Extract: &v1alpha1.DataSource{Key: "kms-json-secret"},
		ReplaceKey: []v1alpha1.ReplaceRule{
			{Source: "^prefix_", Target: ""},
			{Source: "^password$", Target: "secret-password"},
		},
	}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if string(got["username"]) != "alice" {
		t.Fatalf("expected rewritten key username=alice, got %v", got)
	}
	if string(got["secret-password"]) != "p@ss" {
		t.Fatalf("expected rewritten key secret-password, got %v", got)
	}
	for _, unwanted := range []string{"prefix_username", "prefix_password", "password"} {
		if _, has := got[unwanted]; has {
			t.Fatalf("original key %q must be replaced", unwanted)
		}
	}
}

func TestProcessExtractedExternalSecretData_InvalidRuleFailsClosed(t *testing.T) {
	// Fail-closed contract: when a ReplaceKey rule contains an uncompilable
	// regexp, processing must return an error (not silently drop every
	// extracted key), and the error must name the offending pattern so the
	// misconfigured rule can be located.
	payload := []byte(`{"username":"alice"}`)
	data := &v1alpha1.DataProcess{
		Extract: &v1alpha1.DataSource{Key: "kms-json-secret"},
		ReplaceKey: []v1alpha1.ReplaceRule{
			{Source: "([invalid", Target: "x"}, // uncompilable regex
		},
	}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err == nil {
		t.Fatalf("expected an error for an uncompilable ReplaceKey regexp, got result %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil result on error, got %v", got)
	}
	if !strings.Contains(err.Error(), "([invalid") {
		t.Fatalf("error must include the offending regexp, got: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to compile replaceKey regexp") {
		t.Fatalf("error must state the replaceKey regexp compile failure, got: %v", err)
	}
}

func TestProcessExtractedExternalSecretData_InvalidPayload(t *testing.T) {
	// A payload that is neither a valid JSON/YAML mapping is rejected.
	payload := []byte("{not-a-json-payload")
	data := &v1alpha1.DataProcess{Extract: &v1alpha1.DataSource{Key: "bad-doc"}}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err == nil {
		t.Fatalf("expected an error for an unparseable payload, got %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil result on error, got %v", got)
	}
}

func TestProcessExtractedExternalSecretData_LeadingWhitespaceJSONIsYAML(t *testing.T) {
	// The strict JSON probe inspects the raw first byte (no TrimSpace), so a
	// JSON document with leading whitespace is treated as YAML input and
	// re-emitted in YAML style.
	payload := []byte(`  {"a":1}`)
	data := &v1alpha1.DataProcess{Extract: &v1alpha1.DataSource{Key: "kms-json-secret"}}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got["a"]) != "1\n" {
		t.Fatalf("expected YAML-style output for leading-whitespace JSON, got %q", got["a"])
	}
}

func TestProcessExtractedExternalSecretData_TopLevelJSONArrayErrors(t *testing.T) {
	// Pin the current behavior: a top-level JSON array passes the strict JSON
	// probe, but the extract path unmarshals into map[string]interface{}, so
	// an array payload is rejected with an error.
	payload := []byte(`[{"a":1}]`)
	data := &v1alpha1.DataProcess{Extract: &v1alpha1.DataSource{Key: "kms-json-secret"}}

	got, err := ProcessExtractedExternalSecretData(data, payload)
	if err == nil {
		t.Fatalf("expected an error for a top-level JSON array, got %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil result on error, got %v", got)
	}
}

func TestProcessExternalSecretData_JMESPathInvalidJSONFallsBackToKey(t *testing.T) {
	// When jmesPath is configured but the payload is neither valid JSON nor
	// valid YAML (unclosed brace), parsing fails and processing falls back
	// to the non-jmes branch, whose target key is resolved via
	// ResolveTargetKey (name, falling back to key). Here name is empty, so
	// the fallback key must be data.Key.
	payload := []byte("{not-a-json-payload")
	data := &v1alpha1.DataSource{
		Key:  "kms-secret",
		Name: "",
		JMESPath: []v1alpha1.JMESPathObject{
			{Path: "username", ObjectAlias: "user"},
		},
	}

	got, err := ProcessExternalSecretData(data, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if _, empty := got[""]; empty {
		t.Fatalf("produced map must not contain an empty key")
	}
	if v, ok := got["kms-secret"]; !ok || !bytes.Equal(v, payload) {
		t.Fatalf("expected fallback key %q with raw payload, got %v", "kms-secret", got)
	}
	if _, has := got["user"]; has {
		t.Fatalf("ObjectAlias key %q must not appear when jmesPath parsing fails", "user")
	}
}
