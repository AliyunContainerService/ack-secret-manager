package externalsecret

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// TestBcryptFunction tests the bcrypt password hashing function
func TestBcryptFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		password  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:      "bcrypt with default cost",
			template:  `{{ bcrypt "password123" }}`,
			password:  "password123",
			data:      map[string]string{},
			expected:  "$2a$10$", // bcrypt prefix with default cost 10
			expectErr: false,
		},
		{
			name:      "bcrypt with custom cost",
			template:  `{{ bcrypt "password123" 12 }}`,
			password:  "password123",
			data:      map[string]string{},
			expected:  "$2a$12$", // bcrypt prefix with cost 12
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr && !strings.HasPrefix(string(result), tt.expected) {
				t.Errorf("Expected bcrypt hash starting with %q, got %q", tt.expected, string(result))
			}
			// Verify the hash actually round-trips with the input password.
			if !tt.expectErr {
				if err := bcrypt.CompareHashAndPassword(result, []byte(tt.password)); err != nil {
					t.Errorf("bcrypt hash does not verify against input password %q: %v", tt.password, err)
				}
			}
		})
	}
}

// TestHtpasswdFunction tests the htpasswd format generation
func TestHtpasswdFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expectErr bool
	}{
		{
			name:      "htpasswd basic auth",
			template:  `{{ htpasswd "admin" "password123" }}`,
			data:      map[string]string{},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if !tt.expectErr {
				output := string(result)
				if !strings.HasPrefix(output, "admin:") {
					t.Errorf("Expected htpasswd format starting with 'admin:', got %q", output)
					return
				}
				hashPart := strings.TrimPrefix(output, "admin:")
				if !strings.HasPrefix(hashPart, "$2") {
					t.Errorf("Expected bcrypt hash (starting with $2) after username, got %q", hashPart)
					return
				}
				// Verify the bcrypt hash round-trips against the input password.
				if err := bcrypt.CompareHashAndPassword([]byte(hashPart), []byte("password123")); err != nil {
					t.Errorf("htpasswd bcrypt hash does not verify against input password: %v", err)
				}
			}
		})
	}
}

// TestJsonPathFunction tests the JSON path extraction function
func TestJsonPathFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath simple nested access",
			template: `{{ jsonPath .config "database.host" }}`,
			data: map[string]string{
				"config": `{"database": {"host": "localhost", "port": 5432}}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
		{
			name:     "jsonPath array access",
			template: `{{ jsonPath .users "0.name" }}`,
			data: map[string]string{
				"users": `[{"name": "alice"}, {"name": "bob"}]`,
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "jsonPath deep nested",
			template: `{{ jsonPath .data "a.b.c" }}`,
			data: map[string]string{
				"data": `{"a": {"b": {"c": "deep_value"}}}`,
			},
			expected:  "deep_value",
			expectErr: false,
		},
		{
			name:     "jsonPath invalid path",
			template: `{{ jsonPath .data "nonexistent" }}`,
			data: map[string]string{
				"data": `{"key": "value"}`,
			},
			expected:  "",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestJsonPathWithNestedAccess tests jsonPath function with nested object access
func TestJsonPathWithNestedAccess(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "jsonPath nested access with double quotes",
			template: `{{ jsonPath .jsonData "json_object.nested.inner" }}`,
			data: map[string]string{
				"jsonData": `{"json_object": {"nested": {"inner": "deep-value"}}}`,
			},
			expected:  "deep-value",
			expectErr: false,
		},
		{
			name:     "jsonPath array and nested access",
			template: `{{ jsonPath .data "users.0.address.city" }}`,
			data: map[string]string{
				"data": `{"users": [{"name": "alice", "address": {"city": "Beijing"}}]}`,
			},
			expected:  "Beijing",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}

			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestMergeJsonFunction tests the JSON merge function
func TestMergeJsonFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "mergeJson combines two objects",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"a": 1, "b": 2}`,
				"override": `{"b": 3, "c": 4}`,
			},
			expected:  `{"a":1,"b":3,"c":4}`,
			expectErr: false,
		},
		{
			name:     "mergeJson deep merge",
			template: `{{ mergeJson .base .override }}`,
			data: map[string]string{
				"base":     `{"config": {"a": 1, "b": 2}}`,
				"override": `{"config": {"b": 3}}`,
			},
			expected:  `{"config":{"a":1,"b":3}}`,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestParseKeyValueFunction tests the key=value parsing function
func TestParseKeyValueFunction(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "parseKeyValue basic",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY1" }}`,
			data: map[string]string{
				"data": "KEY1=value1\nKEY2=value2",
			},
			expected:  "value1",
			expectErr: false,
		},
		{
			name:     "parseKeyValue with comments",
			template: `{{ $kv := parseKeyValue .data }}{{ index $kv "KEY" }}`,
			data: map[string]string{
				"data": "# Comment\nKEY=value\n# Another comment",
			},
			expected:  "value",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// TestExecuteTemplateWithSprigFunctions tests standard Go template syntax
// combined with Sprig functions still works as expected.
func TestExecuteTemplateWithSprigFunctions(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)

	tests := []struct {
		name      string
		template  string
		data      map[string]string
		expected  string
		expectErr bool
	}{
		{
			name:     "direct field access",
			template: `{{ .username }}`,
			data: map[string]string{
				"username": "alice",
			},
			expected:  "alice",
			expectErr: false,
		},
		{
			name:     "range over data",
			template: `{{ range $k, $v := . }}{{ $k }}={{ $v }};{{ end }}`,
			data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expected:  "", // order-independent; validated below
			expectErr: false,
		},
		{
			name:     "if condition",
			template: `{{ if eq .status "active" }}yes{{ else }}no{{ end }}`,
			data: map[string]string{
				"status": "active",
			},
			expected:  "yes",
			expectErr: false,
		},
		{
			name:     "pipe with Sprig function",
			template: `{{ .name | upper }}`,
			data: map[string]string{
				"name": "alice",
			},
			expected:  "ALICE",
			expectErr: false,
		},
		{
			name:     "fromJson with index",
			template: `{{ index (.config | fromJson) "host" }}`,
			data: map[string]string{
				"config": `{"host": "localhost", "port": 5432}`,
			},
			expected:  "localhost",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map[string]string to map[string]interface{}
			interfaceData := make(map[string]interface{})
			for k, v := range tt.data {
				interfaceData[k] = v
			}
			result, err := stp.executeTemplate(tt.template, interfaceData)
			if (err != nil) != tt.expectErr {
				t.Errorf("Unexpected error state: err=%v, expectErr=%v", err, tt.expectErr)
				return
			}
			// The "range over data" case is order-independent due to Go map
			// iteration order being non-deterministic.
			if tt.name == "range over data" {
				output := string(result)
				parts := strings.Split(strings.TrimSuffix(output, ";"), ";")
				sort.Strings(parts)
				expectedParts := []string{"key1=value1", "key2=value2"}
				if len(parts) != len(expectedParts) {
					t.Errorf("Expected %d key=value pairs, got %d: %q", len(expectedParts), len(parts), output)
					return
				}
				for i, p := range parts {
					if p != expectedParts[i] {
						t.Errorf("Expected sorted pair[%d]=%q, got %q (full output=%q)", i, expectedParts[i], p, output)
					}
				}
				return
			}
			if string(result) != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error grading (parse vs execution, fatal vs non-fatal)
//
// These exercise the template processor in isolation. They complement -- and
// deliberately do NOT duplicate -- the reconcile-level fatal-template contract
// in write_decision_contract_test.go, which drives the full syncIfNeedUpdate
// path. Here the focus is executeTemplate's sentinel grading and how it
// surfaces through ProcessAllTemplates' FatalErrors vs Stats.Errors buckets.
// ---------------------------------------------------------------------------

// TestExecuteTemplateErrorGrading pins the sentinel-based grading of
// executeTemplate: a structurally invalid template wraps errTemplateParse
// (fatal) while a runtime failure wraps errTemplateExecution (non-fatal).
func TestExecuteTemplateErrorGrading(t *testing.T) {
	stp := NewSimpleTemplateProcessor(nil)
	data := map[string]interface{}{"a": "x"}

	tests := []struct {
		name      string
		template  string
		wantParse bool
		wantExec  bool
	}{
		{
			name:      "unclosed action is a parse error (fatal)",
			template:  "{{ .a",
			wantParse: true,
		},
		{
			name:      "missing if operand is a parse error (fatal)",
			template:  "{{ if }}x{{ end }}",
			wantParse: true,
		},
		{
			name:     "field access on a string value is an execution error (non-fatal)",
			template: "{{ .a.b }}",
			wantExec: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stp.executeTemplate(tt.template, data)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if gotParse := stderrors.Is(err, errTemplateParse); gotParse != tt.wantParse {
				t.Errorf("errTemplateParse match = %v, want %v (err=%v)", gotParse, tt.wantParse, err)
			}
			if gotExec := stderrors.Is(err, errTemplateExecution); gotExec != tt.wantExec {
				t.Errorf("errTemplateExecution match = %v, want %v (err=%v)", gotExec, tt.wantExec, err)
			}
		})
	}
}

// TestProcessAllTemplatesErrorGrading pins how executeTemplate's graded errors
// surface through ProcessAllTemplates: a parse error in an inline Template.Data
// entry becomes a FatalError that aborts the round (returned error), while an
// execution error is collected as a non-fatal Stats.Errors entry and the round
// still succeeds with that key skipped.
func TestProcessAllTemplatesErrorGrading(t *testing.T) {
	t.Run("parse error is fatal and aborts the round", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			Data: map[string]string{"out": "{{ if }}"},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"a": []byte("x")})
		if err == nil {
			t.Fatalf("expected a fatal error for a parse failure, got nil")
		}
		if len(result.Stats.FatalErrors) == 0 {
			t.Fatalf("expected FatalErrors to be populated, got %+v", result.Stats)
		}
		if len(result.Stats.Errors) != 0 {
			t.Fatalf("a parse failure must not be graded as a non-fatal error, got %+v", result.Stats.Errors)
		}
	})

	t.Run("execution error is non-fatal and the round succeeds", func(t *testing.T) {
		stp := NewSimpleTemplateProcessor(nil)
		es := buildTestExternalSecret(&api.ExternalSecretTemplate{
			Data: map[string]string{
				"good": "{{ .a }}",
				"bad":  "{{ .a.b }}", // field access on a string -> execution error
			},
		})
		result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"a": []byte("x")})
		if err != nil {
			t.Fatalf("execution errors must be non-fatal, got %v", err)
		}
		if len(result.Stats.FatalErrors) != 0 {
			t.Fatalf("an execution error must not be graded fatal, got %+v", result.Stats.FatalErrors)
		}
		if len(result.Stats.Errors) == 0 {
			t.Fatalf("expected a non-fatal Stats.Errors entry for the failed key, got %+v", result.Stats)
		}
		// The good key still renders; the failed-execution key is skipped.
		if string(result.Data["good"]) != "x" {
			t.Errorf("expected the good key to render as %q, got %q", "x", string(result.Data["good"]))
		}
		if _, ok := result.Data["bad"]; ok {
			t.Errorf("the failed-execution key must be skipped, got %v", result.Data)
		}
	})
}

// ---------------------------------------------------------------------------
// templateFrom Secret source (processSecretTemplate)
//
// Covers the Secret-backed templateFrom source: the template body is read
// from a referenced Secret's data key, rendered against the fetched data and
// placed under the item key (Values scope) or parsed into pairs
// (KeysAndValues scope).
// ---------------------------------------------------------------------------

// newTemplateSourceClient builds a fake client seeded with templateFrom
// source objects (core scheme only is sufficient).
func newTemplateSourceClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// TestProcessSecretTemplateFromSecretRef drives processSecretTemplate directly
// against a fake client holding the template source Secret.
func TestProcessSecretTemplateFromSecretRef(t *testing.T) {
	const ns = "default"
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tpl-src", Namespace: ns},
		Data: map[string][]byte{
			"config.tpl": []byte("user={{ .username }}"),
			"env.tpl":    []byte("DB_HOST={{ .host }}\nDB_PORT=3306"),
			"broken.tpl": []byte("{{ .unclosed"),
		},
	}

	tests := []struct {
		name      string
		ref       *api.TemplateRef
		data      map[string]interface{}
		expected  map[string]string
		errSubstr string
	}{
		{
			name: "renders template read from Secret with context substitution",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl"}},
			},
			data:     map[string]interface{}{"username": "alice"},
			expected: map[string]string{"config.tpl": "user=alice"},
		},
		{
			name: "explicit Values scope behaves like the default",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl", TemplateAs: api.TemplateScopeValues}},
			},
			data:     map[string]interface{}{"username": "bob"},
			expected: map[string]string{"config.tpl": "user=bob"},
		},
		{
			name: "KeysAndValues scope parses rendered key=value lines",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "env.tpl", TemplateAs: api.TemplateScopeKeysAndValues}},
			},
			data: map[string]interface{}{"host": "db.local"},
			expected: map[string]string{
				"DB_HOST": "db.local",
				"DB_PORT": "3306",
			},
		},
		{
			name: "referenced Secret does not exist",
			ref: &api.TemplateRef{
				Name:  "missing-tpl-src",
				Items: []api.TemplateRefItem{{Key: "config.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "failed to get Secret missing-tpl-src",
		},
		{
			name: "key absent from the referenced Secret",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "absent.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "key absent.tpl not found in Secret tpl-src",
		},
		{
			name: "structurally invalid template fails to parse",
			ref: &api.TemplateRef{
				Name:  "tpl-src",
				Items: []api.TemplateRefItem{{Key: "broken.tpl"}},
			},
			data:      map[string]interface{}{},
			errSubstr: "failed to process Secret template key broken.tpl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stp := NewSimpleTemplateProcessor(newTemplateSourceClient(t, srcSecret))
			got, err := stp.processSecretTemplate(context.Background(), tt.ref, ns, tt.data)
			if tt.errSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%v)", tt.errSubstr, got)
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %v", tt.errSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d rendered keys, got %v", len(tt.expected), got)
			}
			for k, want := range tt.expected {
				if string(got[k]) != want {
					t.Errorf("key %q: expected %q, got %q", k, want, string(got[k]))
				}
			}
		})
	}
}

// TestProcessAllTemplatesWithSecretTemplateFrom pins the end-to-end path:
// ProcessAllTemplates resolves a Secret-backed templateFrom entry into the
// Data target and records the rendered key in the managed-keys annotation.
func TestProcessAllTemplatesWithSecretTemplateFrom(t *testing.T) {
	const ns = "default"
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tpl-src", Namespace: ns},
		Data: map[string][]byte{
			"config.tpl": []byte("user={{ .username }}"),
		},
	}
	stp := NewSimpleTemplateProcessor(newTemplateSourceClient(t, srcSecret))
	es := buildTestExternalSecret(&api.ExternalSecretTemplate{
		TemplateFrom: []api.TemplateFrom{
			{
				Secret: &api.TemplateRef{
					Name:  "tpl-src",
					Items: []api.TemplateRefItem{{Key: "config.tpl"}},
				},
				Target: api.TemplateTargetData,
			},
		},
	})

	result, err := stp.ProcessAllTemplates(context.Background(), es, map[string][]byte{"username": []byte("alice")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(result.Data["config.tpl"]); got != "user=alice" {
		t.Fatalf("expected rendered config.tpl=user=alice, got %v", result.Data)
	}
	// Replace mode with a Data-targeted templateFrom clears the raw data.
	if _, ok := result.Data["username"]; ok {
		t.Fatalf("raw data must be cleared in Replace mode, got %v", result.Data)
	}
	if got := result.Metadata.Annotations[ManagedKeysAnnotation]; got != "config.tpl" {
		t.Fatalf("expected managed-keys annotation config.tpl, got %q", got)
	}
	if result.Stats.TemplateFromProcessed != 1 {
		t.Fatalf("expected 1 templateFrom source processed, got %d", result.Stats.TemplateFromProcessed)
	}
}
