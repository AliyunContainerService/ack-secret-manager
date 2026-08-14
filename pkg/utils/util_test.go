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

package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// newTeaSDKError builds a *tea.SDKError with optional Code and StatusCode.
func newTeaSDKError(code *string, statusCode *int) *tea.SDKError {
	return &tea.SDKError{
		Code:       code,
		StatusCode: statusCode,
		Message:    tea.String("test error"),
	}
}

func TestJudgeNeedRetry(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }

	clientErr := func(code string) error {
		err, ok := sdkErr.NewClientError(code, "test client error", nil).(*sdkErr.ClientError)
		if !ok {
			t.Fatalf("NewClientError did not return *ClientError")
		}
		return err
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		// tea.SDKError cases (new darabonba SDK)
		{name: "tea SDKError throttling code", err: newTeaSDKError(strPtr(REJECTED_THROTTLING), intPtr(429)), want: true},
		{name: "tea SDKError service unavailable temporary", err: newTeaSDKError(strPtr(SERVICE_UNAVAILABLE_TEMPORARY), intPtr(503)), want: true},
		{name: "tea SDKError internal failure", err: newTeaSDKError(strPtr(INTERNAL_FAILURE), intPtr(500)), want: true},
		{name: "tea SDKError 5xx status without retryable code", err: newTeaSDKError(strPtr("UnknownError"), intPtr(502)), want: true},
		{name: "tea SDKError 503 status with nil code", err: newTeaSDKError(nil, intPtr(503)), want: true},
		{name: "tea SDKError 403 forbidden", err: newTeaSDKError(strPtr("Forbidden.RAM"), intPtr(403)), want: false},
		{name: "tea SDKError 404 not found", err: newTeaSDKError(strPtr("Forbidden.ResourceNotFound"), intPtr(404)), want: false},
		{name: "tea SDKError invalid parameter", err: newTeaSDKError(strPtr("InvalidParameter"), intPtr(400)), want: false},
		{name: "tea SDKError all nil fields", err: newTeaSDKError(nil, nil), want: false},
		// 429 is retryable via the status fallback even when the error code
		// is not in the whitelist (symmetric with the 5xx fallback).
		{name: "tea SDKError 429 without retryable code", err: newTeaSDKError(strPtr("TooManyRequests"), intPtr(429)), want: true},
		{name: "tea SDKError bare 429 with nil code", err: newTeaSDKError(nil, intPtr(429)), want: true},

		// legacy alibaba-cloud-sdk-go ClientError cases
		{name: "ClientError throttling code", err: clientErr(REJECTED_THROTTLING), want: true},
		{name: "ClientError service unavailable temporary", err: clientErr(SERVICE_UNAVAILABLE_TEMPORARY), want: true},
		{name: "ClientError internal failure", err: clientErr(INTERNAL_FAILURE), want: true},
		{name: "ClientError invalid access key", err: clientErr("InvalidAccessKeyId.NotFound"), want: false},

		// other errors
		{name: "generic error", err: errors.New("some network error"), want: false},
		{name: "nil error", err: nil, want: false},

		// network-level transient errors
		{name: "url.Error timeout", err: &url.Error{Op: "Post", URL: "https://kms.aliyuncs.com", Err: fakeNetError{timeout: true}}, want: true},
		{name: "url.Error non-timeout non-op error", err: &url.Error{Op: "Post", URL: "https://kms.aliyuncs.com", Err: errors.New("bad request body")}, want: false},
		{name: "url.Error wrapping connection reset OpError", err: &url.Error{Op: "Post", URL: "https://kms.aliyuncs.com", Err: newOpError(syscall.ECONNRESET)}, want: true},
		{name: "net.OpError connection reset", err: newOpError(syscall.ECONNRESET), want: true},
		{name: "net.OpError broken pipe", err: newOpError(syscall.EPIPE), want: true},
		{name: "net.OpError timeout", err: &net.OpError{Op: "read", Err: fakeNetError{timeout: true}}, want: true},
		{name: "net.OpError DNS no such host", err: newOpError(&net.DNSError{Err: "no such host", Name: "kms.invalid.aliyuncs.com", IsTimeout: false, IsTemporary: false}), want: false},
		{name: "net.OpError DNS temporary", err: newOpError(&net.DNSError{Err: "server misbehaving", Name: "kms.aliyuncs.com", IsTimeout: false, IsTemporary: true}), want: true},
		{name: "net.OpError other errno (permission denied)", err: newOpError(syscall.EACCES), want: false},
		{name: "net.OpError invalid address", err: &net.OpError{Op: "dial", Err: errors.New("missing address")}, want: false},
		{name: "bare net.Error timeout", err: fakeNetError{timeout: true}, want: true},
		{name: "bare net.Error non-timeout", err: fakeNetError{timeout: false}, want: false},

		// errors wrapped with fmt.Errorf("%w") must still be recognized
		{name: "wrapped tea SDKError throttling code", err: fmt.Errorf("call kms failed: %w", newTeaSDKError(strPtr(REJECTED_THROTTLING), intPtr(429))), want: true},
		{name: "wrapped tea SDKError 403 forbidden", err: fmt.Errorf("call kms failed: %w", newTeaSDKError(strPtr("Forbidden.RAM"), intPtr(403))), want: false},
		{name: "wrapped ClientError throttling code", err: fmt.Errorf("legacy call failed: %w", clientErr(REJECTED_THROTTLING)), want: true},
		{name: "wrapped url.Error timeout", err: fmt.Errorf("outer wrap: %w", &url.Error{Op: "Post", URL: "https://kms.aliyuncs.com", Err: fakeNetError{timeout: true}}), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JudgeNeedRetry(tt.err); got != tt.want {
				t.Errorf("JudgeNeedRetry(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// fakeNetError is a configurable net.Error implementation for tests.
type fakeNetError struct {
	timeout bool
}

func (e fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool   { return e.timeout }
func (e fakeNetError) Temporary() bool { return e.timeout }

// newOpError builds a *net.OpError wrapping the given inner error.
func newOpError(inner error) *net.OpError {
	return &net.OpError{Op: "read", Net: "tcp", Err: inner}
}

// NOTE: the backoff helpers below mutate the package-global
// BACKOFF_DEFAULT_RETRY_INTERVAL, so the tests in this file rely on the
// package's default SERIAL test execution and must never call t.Parallel().

// shortenBackoff shrinks the exponential backoff interval for tests and
// returns a restore function.
func shortenBackoff(t *testing.T) func() {
	t.Helper()
	orig := BACKOFF_DEFAULT_RETRY_INTERVAL
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Millisecond
	return func() { BACKOFF_DEFAULT_RETRY_INTERVAL = orig }
}

// lengthenBackoff raises the backoff so ctx.Done() is the only ready select
// branch; cancellation cases must use it (a ~1ms backoff makes timer/select
// races flaky).
func lengthenBackoff(t *testing.T) func() {
	t.Helper()
	orig := BACKOFF_DEFAULT_RETRY_INTERVAL
	BACKOFF_DEFAULT_RETRY_INTERVAL = 100 * time.Millisecond
	return func() { BACKOFF_DEFAULT_RETRY_INTERVAL = orig }
}

// transientErr returns a *tea.SDKError classified as transient.
func transientErr() error {
	return newTeaSDKError(tea.String(REJECTED_THROTTLING), tea.Int(429))
}

// permanentErr returns a *tea.SDKError classified as permanent.
func permanentErr() error {
	return newTeaSDKError(tea.String("Forbidden.RAM"), tea.Int(403))
}

func TestRetryOnTransient(t *testing.T) {
	t.Run("invalid maxAttempts", func(t *testing.T) {
		for _, maxAttempts := range []int{0, -1} {
			err := RetryOnTransient(context.Background(), maxAttempts, func() error { return nil })
			if err == nil {
				t.Fatalf("expected error for maxAttempts = %d, got nil", maxAttempts)
			}
		}
	})

	t.Run("maxAttempts=1 returns the single attempt error", func(t *testing.T) {
		defer shortenBackoff(t)()
		calls := 0
		err := RetryOnTransient(context.Background(), 1, func() error {
			calls++
			return transientErr()
		})
		if err == nil {
			t.Fatalf("expected error after single attempt, got nil")
		}
		if calls != 1 {
			t.Fatalf("expected exactly 1 call, got %d", calls)
		}
	})

	t.Run("success on first attempt", func(t *testing.T) {
		defer shortenBackoff(t)()
		calls := 0
		err := RetryOnTransient(context.Background(), 3, func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("transient error then success", func(t *testing.T) {
		defer shortenBackoff(t)()
		calls := 0
		err := RetryOnTransient(context.Background(), 3, func() error {
			calls++
			if calls < 3 {
				return transientErr()
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error after retries, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("transient error exhausts all attempts", func(t *testing.T) {
		defer shortenBackoff(t)()
		const maxAttempts = 3
		calls := 0
		err := RetryOnTransient(context.Background(), maxAttempts, func() error {
			calls++
			return transientErr()
		})
		if err == nil {
			t.Fatalf("expected error after exhausting attempts, got nil")
		}
		if calls != maxAttempts {
			t.Fatalf("expected %d calls, got %d", maxAttempts, calls)
		}
		if !JudgeNeedRetry(err) {
			t.Fatalf("expected last transient error to be returned, got %v", err)
		}
	})

	t.Run("permanent error returns immediately", func(t *testing.T) {
		defer shortenBackoff(t)()
		calls := 0
		err := RetryOnTransient(context.Background(), 3, func() error {
			calls++
			return permanentErr()
		})
		if err == nil {
			t.Fatalf("expected permanent error, got nil")
		}
		if calls != 1 {
			t.Fatalf("expected exactly 1 call for permanent error, got %d", calls)
		}
		if JudgeNeedRetry(err) {
			t.Fatalf("expected returned error to be non-retryable, got %v", err)
		}
	})

	t.Run("ctx cancelled while waiting for backoff", func(t *testing.T) {
		// A LONG backoff (not shortenBackoff) guarantees ctx.Done() is the
		// only ready select branch when the cancellation is checked.
		defer lengthenBackoff(t)()
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := RetryOnTransient(ctx, 3, func() error {
			calls++
			if calls == 1 {
				// Cancel before the first backoff wait completes.
				cancel()
			}
			return transientErr()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		// The combined error must also carry the last transient error.
		if !strings.Contains(err.Error(), "last transient error") || !strings.Contains(err.Error(), REJECTED_THROTTLING) {
			t.Fatalf("expected combined error to contain the last transient error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call before cancellation, got %d", calls)
		}
	})

	t.Run("ctx already cancelled before entering the loop", func(t *testing.T) {
		// ctx.Done() is already ready when the loop starts, so a LONG backoff
		// (lengthenBackoff, not shortenBackoff) is required to keep it the
		// only ready select branch.
		defer lengthenBackoff(t)()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := RetryOnTransient(ctx, 3, func() error {
			calls++
			return transientErr()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if !strings.Contains(err.Error(), "last transient error") {
			t.Fatalf("expected combined error to contain the last transient error, got %v", err)
		}
		// The first attempt still runs; cancellation aborts the backoff wait.
		if calls != 1 {
			t.Fatalf("expected 1 call before cancellation, got %d", calls)
		}
	})

	t.Run("wrapped transient error is retried", func(t *testing.T) {
		defer shortenBackoff(t)()
		calls := 0
		err := RetryOnTransient(context.Background(), 3, func() error {
			calls++
			if calls < 2 {
				return fmt.Errorf("fetch from backend failed: %w", transientErr())
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error after retrying wrapped transient error, got %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("network timeout errors retried until success", func(t *testing.T) {
		// End-to-end closed loop: network error -> classified retryable ->
		// retried -> final success.
		defer shortenBackoff(t)()
		const maxAttempts = 3
		calls := 0
		err := RetryOnTransient(context.Background(), maxAttempts, func() error {
			calls++
			if calls < maxAttempts {
				return &url.Error{Op: "Post", URL: "https://kms.aliyuncs.com", Err: fakeNetError{timeout: true}}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error after retrying network timeouts, got %v", err)
		}
		if calls != maxAttempts {
			t.Fatalf("expected %d calls, got %d", maxAttempts, calls)
		}
	})
}

func TestGetWaitTimeExponential(t *testing.T) {
	defer shortenBackoff(t)()
	// Also shrink the capacity so the cap case is reached with small values.
	origCapacity := BACKOFF_DEFAULT_CAPACITY
	BACKOFF_DEFAULT_CAPACITY = 8 * time.Millisecond
	defer func() { BACKOFF_DEFAULT_CAPACITY = origCapacity }()

	tests := []struct {
		retryTimes int
		wantBase   time.Duration
	}{
		{retryTimes: 1, wantBase: 2 * time.Millisecond},
		{retryTimes: 2, wantBase: 4 * time.Millisecond},
		{retryTimes: 3, wantBase: 8 * time.Millisecond},
		{retryTimes: 10, wantBase: BACKOFF_DEFAULT_CAPACITY},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("retryTimes=%d", tt.retryTimes), func(t *testing.T) {
			// With +/-20% jitter the exact value cannot be asserted; check the
			// result stays within [0.8*base, min(1.2*base, capacity)] and never
			// exceeds the capacity cap.
			low := time.Duration(float64(tt.wantBase) * 0.8)
			high := time.Duration(float64(tt.wantBase) * 1.2)
			if high > BACKOFF_DEFAULT_CAPACITY {
				high = BACKOFF_DEFAULT_CAPACITY
			}
			for i := 0; i < 50; i++ {
				got := GetWaitTimeExponential(tt.retryTimes)
				if got > BACKOFF_DEFAULT_CAPACITY {
					t.Fatalf("GetWaitTimeExponential(%d) = %v exceeds capacity %v", tt.retryTimes, got, BACKOFF_DEFAULT_CAPACITY)
				}
				if got < low || got > high {
					t.Fatalf("GetWaitTimeExponential(%d) = %v outside jitter range [%v, %v]", tt.retryTimes, got, low, high)
				}
			}
		})
	}
}

func TestGetWaitTimeExponentialJitterVaries(t *testing.T) {
	defer shortenBackoff(t)()
	// Sample the same retry round repeatedly and require at least two
	// distinct values, proving the jitter is actually applied.
	seen := map[time.Duration]struct{}{}
	for i := 0; i < 100; i++ {
		seen[GetWaitTimeExponential(2)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("expected jitter to produce varying backoff values, got a single value %v", len(seen))
	}
}

// Per-expression failures are logged and skipped (not returned); only a
// structurally invalid secret value returns an error.
func TestGetJsonSecretsInvalidInput(t *testing.T) {
	const secretKey = "test-secret"
	jmesPathObj := []v1alpha1.JMESPathObject{
		{Path: ".username", ObjectAlias: "test"},
	}
	// "{{{" is neither valid YAML nor valid JSON.
	_, err := GetJsonSecrets(jmesPathObj, "{{{", secretKey)
	if err == nil {
		t.Fatalf("expected error for invalid JSON/YAML input, got nil")
	}
	expectedErrorMessage := fmt.Sprintf("invalid JSON or YAML used with jmesPath in secret key: %s", secretKey)
	if err.Error() != expectedErrorMessage {
		t.Fatalf("Expected error: %s, got error: %v", expectedErrorMessage, err)
	}
}

func TestGetJsonSecretsValidJson(t *testing.T) {
	jsonContent := `{"username": "ParameterStoreUser", "password": "PasswordForParameterStore"}`
	jmesPathObj := []v1alpha1.JMESPathObject{
		{Path: "username", ObjectAlias: "user"},
		{Path: "password", ObjectAlias: "pass"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "test-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["user"] != "ParameterStoreUser" || got["pass"] != "PasswordForParameterStore" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestGetJsonSecretsSkipsInvalidExpressions(t *testing.T) {
	jsonContent := `{"username": "ParameterStoreUser"}`
	jmesPathObj := []v1alpha1.JMESPathObject{
		// Invalid syntax: logged and skipped, not returned as an error.
		{Path: ".testpath", ObjectAlias: "badSyntax"},
		// Path resolves to nothing: logged and skipped as well.
		{Path: "testpath", ObjectAlias: "noSuchObject"},
		{Path: "username", ObjectAlias: "user"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "test-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got["user"] != "ParameterStoreUser" {
		t.Fatalf("expected only the valid expression to produce a value, got: %v", got)
	}
}

// TestGetJsonSecretsEmptyObjectAliasFallsBackToKey pins the empty-alias
// guard: an ObjectAlias of "" must fall back to the source data.Key instead
// of emitting an illegal empty Secret key (which the API server would reject
// atomically, failing the whole write). Named aliases stay unaffected.
func TestGetJsonSecretsEmptyObjectAliasFallsBackToKey(t *testing.T) {
	jsonContent := `{"username": "ParameterStoreUser", "password": "secret123"}`
	jmesPathObj := []v1alpha1.JMESPathObject{
		// Empty alias -> falls back to the data.Key passed in.
		{Path: "username", ObjectAlias: ""},
		// Named alias -> used as-is.
		{Path: "password", ObjectAlias: "pass"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "kms-json-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, hasEmpty := got[""]; hasEmpty {
		t.Fatalf("empty ObjectAlias must NOT produce an empty key, got: %v", got)
	}
	if got["kms-json-secret"] != "ParameterStoreUser" {
		t.Fatalf("empty alias must fall back to data.Key, got: %v", got)
	}
	if got["pass"] != "secret123" {
		t.Fatalf("named alias must be preserved, got: %v", got)
	}
}

// TestGetJsonSecretsJSONInputComplexValueStaysJSON is the core reproduction
// case for the "JSON source emitted as YAML" defect: when the fetched secret
// value is JSON and a jmesPath extracts a complex (map/slice) value, the
// output must be compact JSON, not YAML.
func TestGetJsonSecretsJSONInputComplexValueStaysJSON(t *testing.T) {
	jsonContent := `{"name":"xiaoming","age":10,"friends":[{"name":"xiaohong","age":11},{"name":"xiaolan","age":12}]}`
	jmesPathObj := []v1alpha1.JMESPathObject{
		{Path: "friends[0]", ObjectAlias: "firstFriend"},
		// Extracting a whole slice must emit a compact JSON array as well.
		{Path: "friends", ObjectAlias: "allFriends"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "kms-json-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// json.Marshal sorts map keys alphabetically, so the compact JSON form is
	// {"age":11,"name":"xiaohong"}.
	if got["firstFriend"] != `{"age":11,"name":"xiaohong"}` {
		t.Fatalf("expected compact JSON output, got %q", got["firstFriend"])
	}
	// Slice elements keep their order; only the keys inside each map are
	// sorted alphabetically by json.Marshal.
	if got["allFriends"] != `[{"age":11,"name":"xiaohong"},{"age":12,"name":"xiaolan"}]` {
		t.Fatalf("expected compact JSON array output, got %q", got["allFriends"])
	}
}

// TestGetJsonSecretsTopLevelArrayJSONStaysJSON pins that a top-level JSON
// array is detected as strict JSON too, so a jmesPath extracting the whole
// array (identity expression) emits compact JSON instead of YAML.
func TestGetJsonSecretsTopLevelArrayJSONStaysJSON(t *testing.T) {
	jsonContent := `[{"a":1},{"a":2}]`
	jmesPathObj := []v1alpha1.JMESPathObject{
		// "@" is the JMESPath identity expression: returns the whole document.
		{Path: "@", ObjectAlias: "whole"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "kms-json-array-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["whole"] != `[{"a":1},{"a":2}]` {
		t.Fatalf("expected compact JSON output for top-level array, got %q", got["whole"])
	}
}

// TestGetJsonSecretsYAMLInputComplexValueStaysYAML is the regression guard
// for the format fix: YAML input must keep emitting YAML for complex values.
func TestGetJsonSecretsYAMLInputComplexValueStaysYAML(t *testing.T) {
	yamlContent := "name: xiaoming\nage: 10\nfriends:\n  - name: xiaohong\n    age: 11\n"
	jmesPathObj := []v1alpha1.JMESPathObject{
		{Path: "friends[0]", ObjectAlias: "firstFriend"},
	}
	got, err := GetJsonSecrets(jmesPathObj, yamlContent, "kms-yaml-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// yaml.Marshal sorts map keys alphabetically.
	if got["firstFriend"] != "age: 11\nname: xiaohong\n" {
		t.Fatalf("expected YAML output for YAML input, got %q", got["firstFriend"])
	}
}

// TestGetJsonSecretsLeadingWhitespaceJSONTreatedAsYAML pins the strict probe:
// a JSON document with leading whitespace is not detected as strict JSON, so
// it is parsed and re-emitted as YAML (the YAML parser accepts it anyway).
func TestGetJsonSecretsLeadingWhitespaceJSONTreatedAsYAML(t *testing.T) {
	jsonContent := "\n  {\"a\":{\"b\":1}}"
	jmesPathObj := []v1alpha1.JMESPathObject{
		{Path: "a", ObjectAlias: "nested"},
	}
	got, err := GetJsonSecrets(jmesPathObj, jsonContent, "kms-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["nested"] != "b: 1\n" {
		t.Fatalf("expected YAML output for leading-whitespace JSON, got %q", got["nested"])
	}
}

// TestGetJsonSecretsScalarExtractionUnchanged pins that scalar extraction
// (string / number / bool) keeps its current behavior for both JSON and YAML
// sources, regardless of the output-format fix.
func TestGetJsonSecretsScalarExtractionUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "json source",
			content: `{"name":"alice","age":10,"ok":true}`,
		},
		{
			name:    "yaml source",
			content: "name: alice\nage: 10\nok: true\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jmesPathObj := []v1alpha1.JMESPathObject{
				{Path: "name", ObjectAlias: "name"},
				{Path: "age", ObjectAlias: "age"},
				{Path: "ok", ObjectAlias: "ok"},
			}
			got, err := GetJsonSecrets(jmesPathObj, tt.content, "test-secret")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got["name"] != "alice" || got["age"] != "10" || got["ok"] != "true" {
				t.Fatalf("unexpected scalar result: %v", got)
			}
		})
	}
}

func TestIsValidJSON(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{name: "empty input", data: nil, want: false},
		{name: "valid json object", data: []byte(`{"a":1}`), want: true},
		{name: "valid json array", data: []byte(`[{"a":1}]`), want: true},
		{name: "invalid json", data: []byte(`{not-json`), want: false},
		{name: "leading whitespace is not strict json", data: []byte(` {"a":1}`), want: false},
		{name: "leading newline is not strict json", data: []byte("\n{\"a\":1}"), want: false},
		// Strict parsing means the WHOLE input must be one JSON document:
		// trailing garbage after a valid document is rejected.
		{name: "trailing extra content is not strict json", data: []byte(`{"a":1}x`), want: false},
		// Empty containers are still valid JSON documents.
		{name: "empty object is valid json", data: []byte(`{}`), want: true},
		{name: "empty array is valid json", data: []byte(`[]`), want: true},
		{name: "plain string", data: []byte(`plain-secret`), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidJSON(tt.data); got != tt.want {
				t.Fatalf("IsValidJSON(%q) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

// validLabelSelector returns a label selector matching the given key/value pair.
func validLabelSelector(key, value string) *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{key: value},
	}
}

// invalidLabelSelector returns a label selector that LabelSelectorAsSelector
// rejects (unknown operator).
func invalidLabelSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "team", Operator: "NotARealOperator", Values: []string{"a"}},
		},
	}
}

func newNamespace(name string, nsLabels map[string]string) corev1.Namespace {
	return corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: nsLabels},
	}
}

// TestIsNamespaceAllowedForClusterExternalSecret pins the fail-closed
// semantics of the CES namespace matching: a namespace is only allowed when
// it explicitly matches, and both selectors and conditions being empty is the
// only "match all" configuration.
func TestIsNamespaceAllowedForClusterExternalSecret(t *testing.T) {
	ns := newNamespace("team-a-ns", map[string]string{"team": "a"})

	tests := []struct {
		name       string
		selectors  []*metav1.LabelSelector
		conditions []v1alpha1.ClusterExternalSecretCondition
		want       bool
	}{
		{
			name: "both fields empty allows every namespace",
			want: true,
		},
		{
			name:      "only selectors configured and matching",
			selectors: []*metav1.LabelSelector{validLabelSelector("team", "a")},
			want:      true,
		},
		{
			name:      "only selectors configured and not matching is denied (no fall-through allow)",
			selectors: []*metav1.LabelSelector{validLabelSelector("team", "b")},
			want:      false,
		},
		{
			name:      "invalid selector is denied (fail-closed, no fall-through allow)",
			selectors: []*metav1.LabelSelector{invalidLabelSelector()},
			want:      false,
		},
		{
			name:      "nil selector elements contribute no match",
			selectors: []*metav1.LabelSelector{nil, nil},
			want:      false,
		},
		{
			name:      "nil selector with a matching selector still allows",
			selectors: []*metav1.LabelSelector{nil, validLabelSelector("team", "a")},
			want:      true,
		},
		{
			name:      "invalid selector denies even when another selector matches (fail-closed)",
			selectors: []*metav1.LabelSelector{invalidLabelSelector(), validLabelSelector("team", "a")},
			want:      false,
		},
		{
			name:       "empty selector matches every namespace",
			selectors:  []*metav1.LabelSelector{{}},
			conditions: nil,
			want:       true,
		},
		{
			name: "condition namespace name list matching",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{Namespaces: []string{"team-a-ns"}},
			},
			want: true,
		},
		{
			name: "condition namespace name list not matching is denied",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{Namespaces: []string{"other-ns"}},
			},
			want: false,
		},
		{
			name: "condition selector matching",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceSelector: validLabelSelector("team", "a")},
			},
			want: true,
		},
		{
			name: "condition selector not matching is denied",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceSelector: validLabelSelector("team", "b")},
			},
			want: false,
		},
		{
			name: "invalid condition selector is denied (fail-closed)",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceSelector: invalidLabelSelector()},
			},
			want: false,
		},
		{
			name: "invalid condition selector denies even when another condition matches (fail-closed)",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceSelector: invalidLabelSelector()},
				{Namespaces: []string{"team-a-ns"}},
			},
			want: false,
		},
		{
			name: "condition regex matching",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceRegexes: []string{"team-.*"}},
			},
			want: true,
		},
		{
			name: "condition regex is anchored (no substring match)",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				// "team-a" must not match namespace "team-a-ns" as a substring.
				{NamespaceRegexes: []string{"team-a"}},
			},
			want: false,
		},
		{
			name: "condition regex not matching is denied",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceRegexes: []string{"prod-.*"}},
			},
			want: false,
		},
		{
			name: "invalid regex is denied (fail-closed)",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceRegexes: []string{"["}},
			},
			want: false,
		},
		{
			name: "invalid regex denies even when another condition would match (fail-closed)",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{NamespaceRegexes: []string{"["}},
				{Namespaces: []string{"team-a-ns"}},
			},
			want: false,
		},
		{
			name:      "invalid legacy selector denies even when a condition matches (fail-closed)",
			selectors: []*metav1.LabelSelector{invalidLabelSelector()},
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{Namespaces: []string{"team-a-ns"}},
			},
			want: false,
		},
		{
			name:      "selectors not matching with conditions matching is allowed",
			selectors: []*metav1.LabelSelector{validLabelSelector("team", "b")},
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				{Namespaces: []string{"team-a-ns"}},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ces := &v1alpha1.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ces"},
				Spec: v1alpha1.ClusterExternalSecretSpec{
					NamespaceSelectors: tt.selectors,
					Conditions:         tt.conditions,
				},
			}
			if got := IsNamespaceAllowedForClusterExternalSecret(ces, ns); got != tt.want {
				t.Errorf("IsNamespaceAllowedForClusterExternalSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeNamespaceGetter returns a getClient function that fills the requested
// namespace object with the given labels, or fails when failGet is true.
func fakeNamespaceGetter(nsLabels map[string]string, failGet bool) func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if failGet {
			return fmt.Errorf("api server unavailable")
		}
		n, ok := obj.(*corev1.Namespace)
		if !ok {
			return fmt.Errorf("unexpected object type %T", obj)
		}
		n.Name = key.Name
		n.Labels = nsLabels
		return nil
	}
}

// TestIsNamespaceAllowedForClusterSecretStore pins the fail-closed semantics
// of the ClusterSecretStore namespace access check: invalid selectors are
// denied just like invalid regexes, and configured-but-non-matching
// conditions deny access.
func TestIsNamespaceAllowedForClusterSecretStore(t *testing.T) {
	const namespaceName = "team-a-ns"
	nsLabels := map[string]string{"team": "a"}

	tests := []struct {
		name       string
		conditions []v1alpha1.ClusterSecretStoreCondition
		getClient  func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
		want       bool
	}{
		{
			name:      "no conditions allows every namespace",
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name: "namespace name list matching",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{Namespaces: []string{namespaceName}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name: "namespace name list not matching is denied",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{Namespaces: []string{"other-ns"}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
		},
		{
			name: "selector matching",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceSelector: validLabelSelector("team", "a")},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name: "selector not matching is denied",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceSelector: validLabelSelector("team", "b")},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
		},
		{
			name: "invalid selector is denied (fail-closed, aligned with invalid regex)",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceSelector: invalidLabelSelector()},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
		},
		{
			name: "invalid selector denies even when another condition would match (fail-closed)",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceSelector: invalidLabelSelector()},
				{Namespaces: []string{namespaceName}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
		},
		{
			name: "invalid regex is denied (fail-closed)",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceRegexes: []string{"["}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
		},
		{
			name: "regex matching",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceRegexes: []string{"team-.*"}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name: "get namespace failure is denied (fail-closed)",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{Namespaces: []string{namespaceName}},
			},
			getClient: fakeNamespaceGetter(nsLabels, true),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			css := &v1alpha1.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "test-css"},
				Spec:       v1alpha1.ClusterSecretStoreSpec{Conditions: tt.conditions},
			}
			if got := IsNamespaceAllowedForClusterSecretStore(css, namespaceName, tt.getClient); got != tt.want {
				t.Errorf("IsNamespaceAllowedForClusterSecretStore() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRemove pins the new filter semantics of Remove: it returns a new slice
// (the input is never mutated) and removes every occurrence including
// adjacent duplicates, which the old in-place single-pass loop skipped.
func TestRemove(t *testing.T) {
	tests := []struct {
		name string
		list []string
		s    string
		want []string
	}{
		{name: "adjacent duplicates are all removed", list: []string{"a", "x", "x", "b"}, s: "x", want: []string{"a", "b"}},
		{name: "all occurrences removed", list: []string{"x", "a", "x", "b", "x"}, s: "x", want: []string{"a", "b"}},
		{name: "no occurrence keeps all elements", list: []string{"a", "b"}, s: "x", want: []string{"a", "b"}},
		{name: "empty list stays empty", list: []string{}, s: "x", want: []string{}},
		{name: "single element removed", list: []string{"x"}, s: "x", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := make([]string, len(tt.list))
			copy(original, tt.list)
			got := Remove(tt.list, tt.s)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Remove(%v, %q) = %v, want %v", original, tt.s, got, tt.want)
			}
			// The input slice must never be mutated in place.
			if !reflect.DeepEqual(tt.list, original) {
				t.Errorf("Remove mutated its input: got %v, want %v", tt.list, original)
			}
		})
	}
}
