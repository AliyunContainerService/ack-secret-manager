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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// Log capture changes global klog state, so these tests must not run in parallel.
func captureSecretLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var flags flag.FlagSet
	klog.InitFlags(&flags)
	previous := flags.Lookup("logtostderr").Value.String()
	if err := flags.Set("logtostderr", "false"); err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	klog.SetOutput(buf)
	t.Cleanup(func() {
		klog.Flush()
		klog.SetOutput(os.Stderr)
		_ = flags.Set("logtostderr", previous)
	})
	return buf
}

func TestSecretSafetyErrorClassification(t *testing.T) {
	const canary = "FAKE_ERROR_SECRET_CANARY"
	for _, raw := range []error{
		&url.Error{Op: "Post", URL: "https://sts.invalid/?OIDCToken=" + canary, Err: context.DeadlineExceeded},
		&tea.SDKError{Code: tea.String(REJECTED_THROTTLING), Message: tea.String(canary)},
		&url.Error{Op: "Post", URL: "https://sts.invalid/?SecurityToken=" + canary, Err: &net.DNSError{Err: "no such host", Name: "sts.invalid"}},
	} {
		err := SanitizeError(raw, "operation failed")
		if !errors.Is(err, raw) || JudgeNeedRetry(raw) != JudgeNeedRetry(err) {
			t.Fatal("sanitization changed error identity or retry classification")
		}
		for _, format := range []string{"%s", "%q", "%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, err); strings.Contains(got, canary) || !strings.Contains(got, "operation failed") {
				t.Errorf("unsafe error formatting with %s", format)
			}
		}
	}
	if SanitizeError(nil, "operation failed") != nil {
		t.Fatal("nil error must stay nil")
	}
}

func TestSecretSafetyErrorCategories(t *testing.T) {
	const canary = "FAKE_CATEGORY_SECRET_CANARY"
	resource := schema.GroupResource{Resource: "secrets"}
	var decoded interface{}
	syntaxErr := json.Unmarshal([]byte(`{"token":`), &decoded)
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"canceled", fmt.Errorf("%s: %w", canary, context.Canceled), "canceled"},
		{"deadline", fmt.Errorf("%s: %w", canary, context.DeadlineExceeded), "timeout"},
		{"forbidden", apierrors.NewForbidden(resource, canary, errors.New(canary)), "forbidden"},
		{"unauthorized", apierrors.NewUnauthorized(canary), "unauthorized"},
		{"conflict", apierrors.NewConflict(resource, canary, errors.New(canary)), "conflict"},
		{"exists", apierrors.NewAlreadyExists(resource, canary), "already_exists"},
		{"not-found", apierrors.NewNotFound(resource, canary), "not_found"},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, "target", field.ErrorList{field.Invalid(field.NewPath("metadata", "labels"), canary, "invalid")}), "invalid"},
		{"bad-request", apierrors.NewBadRequest(canary), "bad_request"},
		{"throttled", apierrors.NewTooManyRequests(canary, 1), "throttled"},
		{"unavailable", apierrors.NewServiceUnavailable(canary), "unavailable"},
		{"internal", apierrors.NewInternalError(errors.New(canary)), "internal"},
		{"api-timeout", apierrors.NewTimeoutError(canary, 1), "timeout"},
		{"api-server-timeout", apierrors.NewServerTimeout(resource, canary, 1), "timeout"},
		{"unknown-api", &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReason(canary), Message: canary}}, "unknown"},
		{"permission", &os.PathError{Op: "open", Path: canary, Err: os.ErrPermission}, "permission_denied"},
		{"missing-file", &os.PathError{Op: "open", Path: canary, Err: os.ErrNotExist}, "not_found"},
		{"file-io", &os.PathError{Op: "read", Path: canary, Err: errors.New(canary)}, "file_io"},
		{"dns", &url.Error{Op: "POST", URL: "https://sts.invalid/?token=" + canary, Err: &net.DNSError{Err: canary, Name: canary}}, "dns"},
		{"network-timeout", &net.DNSError{Err: canary, Name: canary, IsTimeout: true}, "timeout"},
		{"url", &url.Error{Op: "POST", URL: "https://sts.invalid/?token=" + canary, Err: errors.New(canary)}, "network"},
		{"network", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(canary)}, "network"},
		{"sdk-throttled", &tea.SDKError{Code: tea.String(REJECTED_THROTTLING), Message: tea.String(canary)}, "throttled"},
		{"sdk-unavailable", &tea.SDKError{Code: tea.String(SERVICE_UNAVAILABLE_TEMPORARY), Message: tea.String(canary)}, "unavailable"},
		{"sdk-internal", &tea.SDKError{Code: tea.String(INTERNAL_FAILURE), Message: tea.String(canary)}, "internal"},
		{"sdk-http", &tea.SDKError{Code: tea.String(canary), StatusCode: tea.Int(403), Message: tea.String(canary)}, "forbidden"},
		{"sdk-unknown", &tea.SDKError{Code: tea.String(canary), Message: tea.String(canary)}, "unknown"},
		{"sdk-empty", &tea.SDKError{}, "unknown"},
		{"legacy-sdk", sdkErr.NewClientError(REJECTED_THROTTLING, canary, errors.New(canary)), "throttled"},
		{"legacy-unknown", sdkErr.NewClientError(canary, canary, errors.New(canary)), "unknown"},
		{"legacy-server-code", sdkErr.NewServerError(400, `{"Code":"Throttling"}`, canary), "throttled"},
		{"legacy-server-http", sdkErr.NewServerError(503, "", canary), "unavailable"},
		{"legacy-server-5xx", sdkErr.NewServerError(500, "", canary), "server_error"},
		{"legacy-server-unknown", sdkErr.NewServerError(200, "", canary), "unknown"},
		{"json-syntax", syntaxErr, "invalid_json"},
		{"json-type", &json.UnmarshalTypeError{Value: canary, Type: reflect.TypeOf(0)}, "type_mismatch"},
		{"yaml-type", &yaml.TypeError{Errors: []string{canary}}, "type_mismatch"},
		{"unknown", errors.New(canary), "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, raw := range []error{tt.err, fmt.Errorf("%s: %w", canary, tt.err)} {
				wrapped := SanitizeError(raw, "operation failed")
				nested := SanitizeError(wrapped, "outer operation failed")
				for _, safe := range []error{wrapped, nested} {
					if got := SafeErrorCategory(safe); got != tt.want {
						t.Errorf("category = %q, want %q", got, tt.want)
					}
					if !errors.Is(safe, raw) || JudgeNeedRetry(safe) != JudgeNeedRetry(raw) || apierrors.ReasonForError(safe) != apierrors.ReasonForError(raw) {
						t.Error("error identity, retry behavior or API classification changed")
					}
					for _, format := range []string{"%s", "%q", "%v", "%+v", "%#v"} {
						text := fmt.Sprintf(format, safe)
						if strings.Contains(text, canary) || !strings.Contains(text, "operation failed: "+tt.want) {
							t.Errorf("unsafe or incomplete diagnostics with %s", format)
						}
					}
				}
			}
		})
	}
	if SafeErrorCategory(nil) != "none" {
		t.Error("nil must have no error category")
	}
	for status, want := range map[int]string{400: "bad_request", 401: "unauthorized", 403: "forbidden", 404: "not_found", 409: "conflict", 429: "throttled", 503: "unavailable", 502: "server_error", 200: "unknown", 600: "unknown"} {
		if got := SafeErrorCategory(&tea.SDKError{Code: tea.String(canary), StatusCode: tea.Int(status)}); got != want {
			t.Errorf("HTTP %d: category = %q, want %q", status, got, want)
		}
	}
}

type secretSafetyMarshaler struct{}

func (secretSafetyMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("FAKE_MARSHAL_SECRET_CANARY")
}
func (secretSafetyMarshaler) MarshalYAML() (interface{}, error) {
	return nil, errors.New("FAKE_MARSHAL_SECRET_CANARY")
}

func TestSecretSafetyMarshalErrors(t *testing.T) {
	value := secretSafetyMarshaler{}
	if _, err := json.Marshal(value); err == nil || !strings.Contains(err.Error(), "FAKE_MARSHAL") {
		t.Fatal("invalid JSON error fixture")
	}
	if _, err := yaml.Marshal(value); err == nil || !strings.Contains(err.Error(), "FAKE_MARSHAL") {
		t.Fatal("invalid YAML error fixture")
	}
	logs := captureSecretLogs(t)
	if JsonStr(value) != "" || YamlStr(value) != "" {
		t.Fatal("serialization fallback changed")
	}
	klog.Flush()
	if strings.Contains(logs.String(), "FAKE_MARSHAL") || !strings.Contains(logs.String(), "failed to marshal") {
		t.Error("serialization log exposes raw error")
	}
}

func TestSummarizeJMESPathErrorSyntaxClassification(t *testing.T) {
	_, err := jmespath.Search("[?foo", map[string]interface{}{"foo": "bar"})
	var syntaxErr jmespath.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("预期值类型 SyntaxError，实际为 %T", err)
	}
	if got := summarizeJMESPathError(err); got != "invalid expression syntax" {
		t.Fatalf("语法错误摘要不符：%q", got)
	}
	if got := summarizeJMESPathError(errors.New("sensitive input")); got != "expression evaluation failed" {
		t.Fatalf("求值错误摘要不符：%q", got)
	}
}

func TestGetJsonSecretsNoSecretLeakInJMESPathTypeErrorLog(t *testing.T) {
	const sensitiveValue = "SENSITIVE-TOKEN-ABC-9f23c4a1"
	for _, payload := range []string{
		fmt.Sprintf(`{"token":%q,"safe":"ok"}`, sensitiveValue),
		"token: " + sensitiveValue + "\nsafe: ok\n",
	} {
		t.Run(payload[:1], func(t *testing.T) {
			var data interface{}
			if err := yaml.Unmarshal([]byte(payload), &data); err != nil {
				t.Fatal(err)
			}
			// Verify that the fixture triggers a third-party error containing the sensitive value to avoid false positives.
			_, probeErr := jmespath.Search("abs(token)", data)
			if probeErr == nil || !strings.Contains(probeErr.Error(), sensitiveValue) {
				t.Fatal("夹具未触发包含敏感值的类型错误")
			}
			logs := captureSecretLogs(t)
			got, err := GetJsonSecrets([]v1alpha1.JMESPathObject{
				{Path: "abs(token)", ObjectAlias: "bad"},
				{Path: "safe", ObjectAlias: "good"},
			}, payload, "my-secret-key")
			klog.Flush()
			if err != nil || !reflect.DeepEqual(got, map[string]string{"good": "ok"}) {
				t.Fatalf("跳过失败项并保留成功项的行为发生变化：%v，%v", got, err)
			}
			for _, forbidden := range []string{sensitiveValue, "Invalid type for:", ", error:"} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatalf("日志包含禁止输出的内容 %q", forbidden)
				}
			}
			for _, want := range []string{"jmesPath evaluation failed", "path: abs(token)", "secret key: my-secret-key", "reason: expression evaluation failed"} {
				if !strings.Contains(logs.String(), want) {
					t.Fatalf("日志缺少安全诊断信息 %q", want)
				}
			}
		})
	}
}

func TestGetJsonSecretsJSONMarshalFailurePartialSuccess(t *testing.T) {
	const sensitiveValue = "SENSITIVE-ACCOUNT-PASSWORD-7c1b02"
	const path = "map(&sum(balances), accounts)"
	payload := fmt.Sprintf(`{"password":%q,"accounts":[{"name":"alpha","balances":[1e308,1e308]},{"name":"beta","balances":[1.5,2.5]}]}`, sensitiveValue)
	var data interface{}
	if err := yaml.Unmarshal([]byte(payload), &data); err != nil {
		t.Fatal(err)
	}
	overflow, err := jmespath.Search(path, data)
	if err != nil {
		t.Fatal(err)
	}
	values, ok := overflow.([]interface{})
	if !ok || len(values) == 0 {
		t.Fatal("夹具必须产生非空数组")
	}
	if first, ok := values[0].(float64); !ok || !math.IsInf(first, 1) {
		t.Fatal("夹具必须产生 +Inf 以触发 JSON 序列化失败")
	}
	if _, err := json.Marshal(overflow); err == nil {
		t.Fatal("JSON 序列化应拒绝 +Inf")
	}
	logs := captureSecretLogs(t)
	got, err := GetJsonSecrets([]v1alpha1.JMESPathObject{
		{Path: path, ObjectAlias: "totals"},
		{Path: "map(&name, accounts)", ObjectAlias: "names"},
	}, payload, "kms-json-secret")
	klog.Flush()
	if err != nil || !reflect.DeepEqual(got, map[string]string{"names": `["alpha","beta"]`}) {
		t.Fatalf("序列化失败后应保留成功项：%v，%v", got, err)
	}
	for _, forbidden := range []string{sensitiveValue, "alpha", "1e+308", "+Inf", ", error:"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("日志包含禁止输出的内容 %q", forbidden)
		}
	}
	for _, want := range []string{"failed to marshal jmesPath value to JSON", "secret key: kms-json-secret", "object alias: totals", "type: []interface {}"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("日志缺少安全诊断信息 %q", want)
		}
	}
}

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

	serverErr := func(status int, code string) error {
		content := ""
		if code != "" {
			content = `{"Code":"` + code + `"}`
		}
		err, ok := sdkErr.NewServerError(status, content, "test server error").(*sdkErr.ServerError)
		if !ok {
			t.Fatalf("NewServerError did not return *ServerError")
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

		// legacy alibaba-cloud-sdk-go ServerError cases (distinct type with
		// the same code + 5xx/429 fallback as tea.SDKError)
		{name: "ServerError throttling code", err: serverErr(400, REJECTED_THROTTLING), want: true},
		{name: "ServerError 500 status fallback", err: serverErr(500, ""), want: true},
		{name: "ServerError 429 status fallback", err: serverErr(429, "TooManyRequests"), want: true},
		{name: "ServerError 403 permanent", err: serverErr(403, "Forbidden.RAM"), want: false},
		{name: "wrapped ServerError 503", err: fmt.Errorf("legacy call failed: %w", serverErr(503, "")), want: true},

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
			name: "condition regex matches as substring",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				// "team-a" matches namespace "team-a-ns" as a substring,
				// consistent with upstream ESO regexp.MatchString semantics.
				{NamespaceRegexes: []string{"team-a"}},
			},
			want: true,
		},
		{
			name: "condition regex with anchor denies non-matching",
			conditions: []v1alpha1.ClusterExternalSecretCondition{
				// User-supplied ^...$ is respected.
				{NamespaceRegexes: []string{"^team-a$"}},
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
		namespace  string // empty means the default namespaceName
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
			name: "condition regex matches as substring (CSS)",
			// "team-a" matches namespace "team-a-ns" as a substring,
			// consistent with upstream ESO regexp.MatchString semantics.
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceRegexes: []string{"team-a"}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name:      "condition regex exact match allowed",
			namespace: "team-a",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				{NamespaceRegexes: []string{"team-a"}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      true,
		},
		{
			name:      "condition regex with anchor denies suffix",
			namespace: "evil-team-a",
			conditions: []v1alpha1.ClusterSecretStoreCondition{
				// User-supplied ^...$ is respected.
				{NamespaceRegexes: []string{"^team-a$"}},
			},
			getClient: fakeNamespaceGetter(nsLabels, false),
			want:      false,
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
			ns := tt.namespace
			if ns == "" {
				ns = namespaceName
			}
			css := &v1alpha1.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "test-css"},
				Spec:       v1alpha1.ClusterSecretStoreSpec{Conditions: tt.conditions},
			}
			if got := IsNamespaceAllowedForClusterSecretStore(css, ns, tt.getClient); got != tt.want {
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
