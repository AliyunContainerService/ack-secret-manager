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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	BinaryType               = "binary"
	oidcProviderNameTemplate = "ack-rrsa-%s"
)

// TriggerReconcileAnnotation forces store client recreation when set on a
// SecretStore/ClusterSecretStore; shared by the store controllers and the
// Secret/ServiceAccount trigger controllers so the literal never drifts.
const TriggerReconcileAnnotation = "ack-secret-manager.alibabacloud.com/trigger-reconcile"

// PatchTriggerAnnotation patches the trigger annotation onto the given store
// so its controller recreates the provider clients; the value is the current
// unix-nanosecond timestamp, so consecutive calls always produce a change.
// Callers must skip stores already carrying a non-empty trigger annotation
// (a pending rebuild is already guaranteed) to avoid amplifying rebuilds.
func PatchTriggerAnnotation(ctx context.Context, c client.Client, store client.Object) error {
	updatedStore := store.DeepCopyObject().(client.Object)
	annotations := updatedStore.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[TriggerReconcileAnnotation] = strconv.FormatInt(time.Now().UnixNano(), 10)
	updatedStore.SetAnnotations(annotations)

	return c.Patch(ctx, updatedStore, client.MergeFrom(store))
}

// ReadyConditionObservedGeneration locates the condition whose Type is
// SecretStoreReady and returns its ObservedGeneration. found is false when
// no such condition exists; callers treat that as "never managed by a Store
// controller" rather than assuming any particular list position.
func ReadyConditionObservedGeneration(conditions []v1alpha1.SecretStoreStatusCondition) (observed int64, found bool) {
	for i := range conditions {
		if conditions[i].Type == v1alpha1.SecretStoreReady {
			return conditions[i].ObservedGeneration, true
		}
	}
	return 0, false
}

const (
	REJECTED_THROTTLING           = "Rejected.Throttling"
	SERVICE_UNAVAILABLE_TEMPORARY = "ServiceUnavailableTemporary"
	INTERNAL_FAILURE              = "InternalFailure"
)

var (
	// reRoleArn         = regexp.MustCompile(`^acs:ram:[^:]*:\d+:role/[^/]+$`)
	reOidcProviderArn = regexp.MustCompile(`^acs:ram:[^:]*:\d+:oidc-provider/[^/]+$`)
)

var (
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Second
	BACKOFF_DEFAULT_CAPACITY       = time.Duration(10) * time.Second
)

// IsNamespaceWatched reports whether the namespace falls inside the watch
// scope built from --watch-namespaces / --exclude-namespaces (a map with
// true entries for watched and false entries for excluded namespaces).
// Include mode (any true entry present): only explicitly listed namespaces
// are watched. Exclude-only mode: a namespace is excluded only when mapped
// to false; an empty map or a missing key passes.
func IsNamespaceWatched(watchNs map[string]bool, namespace string) bool {
	for _, watch := range watchNs {
		if watch {
			// Include mode: unlisted namespaces are not watched.
			return watchNs[namespace]
		}
	}
	watch, excluded := watchNs[namespace]
	return !excluded || watch
}

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Remove returns a new slice with all occurrences of s removed; the input
// slice is never modified in place.
func Remove(list []string, s string) []string {
	result := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			result = append(result, v)
		}
	}
	return result
}

func GetConfigFromSecret(ctx context.Context, r client.Client, secretRef *v1alpha1.SecretRef) ([]byte, error) {
	if secretRef == nil {
		return nil, fmt.Errorf("empty secretRef")
	}
	if secretRef.Key == "" || secretRef.Name == "" || secretRef.Namespace == "" {
		return nil, fmt.Errorf("empty secretRef")
	}
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}, secret)
	if err != nil {
		return nil, err
	}
	data, ok := secret.Data[secretRef.Key]
	if !ok {
		return nil, fmt.Errorf("key %v not found", secretRef.Key)
	}
	return data, nil
}

type safeError struct {
	message string
	cause   error
}

func (e *safeError) Error() string { return e.message }
func (e *safeError) Unwrap() error { return e.cause }
func (e *safeError) Format(s fmt.State, verb rune) {
	if verb == 'q' {
		_, _ = fmt.Fprintf(s, "%q", e.message)
		return
	}
	_, _ = fmt.Fprint(s, e.message)
}

// SafeErrorCategory returns only fixed categories; messages, URLs, paths and
// arbitrary SDK codes must never be copied into public diagnostics. This is
// diagnostic classification only and does not change retry decisions.
func SafeErrorCategory(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return "timeout"
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsUnauthorized(err):
		return "unauthorized"
	case apierrors.IsConflict(err):
		return "conflict"
	case apierrors.IsAlreadyExists(err):
		return "already_exists"
	case apierrors.IsNotFound(err), errors.Is(err, os.ErrNotExist):
		return "not_found"
	case apierrors.IsInvalid(err):
		return "invalid"
	case apierrors.IsBadRequest(err):
		return "bad_request"
	case apierrors.IsTooManyRequests(err):
		return "throttled"
	case apierrors.IsServiceUnavailable(err):
		return "unavailable"
	case apierrors.IsInternalError(err):
		return "internal"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	}

	var sdkError *tea.SDKError
	if errors.As(err, &sdkError) && sdkError != nil {
		return safeSDKErrorCategory(tea.StringValue(sdkError.Code), tea.IntValue(sdkError.StatusCode))
	}
	var clientError *sdkErr.ClientError
	if errors.As(err, &clientError) && clientError != nil {
		return safeSDKErrorCategory(clientError.ErrorCode(), 0)
	}
	// ServerError is a separate legacy SDK type (it does not embed
	// ClientError), so it needs its own errors.As branch to keep the
	// status-code classification.
	var serverError *sdkErr.ServerError
	if errors.As(err, &serverError) && serverError != nil {
		return safeSDKErrorCategory(serverError.ErrorCode(), serverError.HttpStatus())
	}
	var netError net.Error
	if errors.As(err, &netError) && netError.Timeout() {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns"
	}
	var urlError *url.Error
	var opError *net.OpError
	if errors.As(err, &urlError) || errors.As(err, &opError) {
		return "network"
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return "file_io"
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return "invalid_json"
	}
	var jsonTypeError *json.UnmarshalTypeError
	var yamlTypeError *yaml.TypeError
	if errors.As(err, &jsonTypeError) || errors.As(err, &yamlTypeError) {
		return "type_mismatch"
	}
	return "unknown"
}

func safeSDKErrorCategory(code string, status int) string {
	switch code {
	case REJECTED_THROTTLING, "Throttling", "Throttling.User", "Throttling.Api":
		return "throttled"
	case SERVICE_UNAVAILABLE_TEMPORARY:
		return "unavailable"
	case INTERNAL_FAILURE:
		return "internal"
	}
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "throttled"
	case http.StatusServiceUnavailable:
		return "unavailable"
	}
	if status >= 500 && status <= 599 {
		return "server_error"
	}
	return "unknown"
}

// SanitizeError preserves the cause for classification, not for logging.
// The operation message must be safe; callers must not log the unwrapped cause.
func SanitizeError(err error, message string) error {
	if err == nil {
		return nil
	}
	return &safeError{message: message + ": " + SafeErrorCategory(err), cause: err}
}

func JsonStr(o interface{}) string {
	temp, ok := o.(string)
	if ok {
		return temp
	}
	str, err := json.Marshal(o)
	if err != nil {
		klog.Errorf("failed to marshal %T to json", o)
		return ""
	}
	return string(str)
}

func YamlStr(o interface{}) string {
	temp, ok := o.(string)
	if ok {
		return temp
	}
	str, err := yaml.Marshal(o)
	if err != nil {
		klog.Errorf("failed to marshal %T to yaml", o)
		return ""
	}
	return string(str)
}

// Ignore not found errors
func IgnoreNotFoundError(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// IsValidJSON reports whether data is a strict JSON document: the first byte
// must be '{' or '[' (leading whitespace is treated as YAML by design).
func IsValidJSON(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	firstChar := data[0]
	if firstChar != '{' && firstChar != '[' {
		return false
	}
	var js interface{}
	return json.Unmarshal(data, &js) == nil
}

// summarizeJMESPathError returns fixed summaries so evaluator errors cannot expose Secret values.
func summarizeJMESPathError(err error) string {
	var syntaxErr jmespath.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "invalid expression syntax"
	}
	return "expression evaluation failed"
}

// GetJsonSecrets evaluates jmesPath expressions against the fetched secret
// value (JSON or YAML). Per-expression failures are logged and skipped; the
// only returned error is a structurally invalid secret value.
func GetJsonSecrets(jmesObj []v1alpha1.JMESPathObject, secretValue, key string) (jsonMap map[string]string, err error) {
	jsonMap = make(map[string]string, 0)
	var data interface{}
	// The output format of complex values follows the INPUT format, not the
	// parser that succeeded (JSON is a YAML subset, so YAML parsing of JSON
	// always succeeds): JSON input keeps emitting JSON, YAML input YAML.
	marshalToYaml := !IsValidJSON([]byte(secretValue))
	if err = yaml.Unmarshal([]byte(secretValue), &data); err != nil {
		if err = json.Unmarshal([]byte(secretValue), &data); err != nil {
			return nil, fmt.Errorf("invalid JSON or YAML used with jmesPath in secret key: %s", key)
		}
	}

	for _, jmesPathEntry := range jmesObj {
		jsonSecret, err := jmespath.Search(jmesPathEntry.Path, data)
		if err != nil {
			klog.Errorf("jmesPath evaluation failed, path: %s, secret key: %s, reason: %s", jmesPathEntry.Path, key, summarizeJMESPathError(err))
			continue
		}

		if jsonSecret == nil {
			klog.Errorf("jmesPath - %s for object alias - %s does not point to a valid object, secret key: %s",
				jmesPathEntry.Path, jmesPathEntry.ObjectAlias, key)
			continue
		}

		var strValue string
		switch v := jsonSecret.(type) {
		case string:
			strValue = v
		case int, int64, uint, uint64, float32, float64, bool:
			strValue = fmt.Sprintf("%v", v)
		case map[string]interface{}, []interface{}:
			// Marshal complex types (maps, slices) to YAML or JSON
			if marshalToYaml {
				yamlData, err := yaml.Marshal(v)
				if err != nil {
					klog.Errorf("failed to marshal jmesPath value to YAML, secret key: %s, object alias: %v, type: %T", key, jmesPathEntry.ObjectAlias, v)
					continue
				}
				strValue = string(yamlData)
			} else {
				jsonData, err := json.Marshal(v)
				if err != nil {
					klog.Errorf("failed to marshal jmesPath value to JSON, secret key: %s, object alias: %v, type: %T", key, jmesPathEntry.ObjectAlias, v)
					continue
				}
				strValue = string(jsonData)
			}

		default:
			klog.Errorf("unsupported jmesPath value type, secret key: %s, object alias: %v, type: %T", key, jmesPathEntry.ObjectAlias, v)
			continue
		}

		// Empty ObjectAlias would produce an illegal empty Secret key rejected
		// by the API server; fall back to the source data.Key (mirrors
		// ResolveTargetKey).
		targetKey := jmesPathEntry.ObjectAlias
		if targetKey == "" {
			targetKey = key
		}
		jsonMap[targetKey] = strValue
	}

	return jsonMap, nil
}

// RewriteRegexp applies one regexp rewrite rule. An uncompilable Source is
// a configuration error returned to the caller (fail closed), not silently
// dropped.
func RewriteRegexp(operation v1alpha1.ReplaceRule, in map[string]string) (map[string]string, error) {
	out := make(map[string]string)
	re, err := regexp.Compile(operation.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to compile replaceKey regexp %q: %v", operation.Source, err)
	}
	for key, value := range in {
		newKey := re.ReplaceAllString(key, operation.Target)
		if _, exists := out[newKey]; exists {
			// Key collision: keep overwrite semantics but surface the data loss
			klog.Warning("key conflict after regex rewrite: multiple source keys map to the same output key, overwriting")
		}
		out[newKey] = value
	}
	return out, nil
}

// JudgeNeedRetry reports whether err is transient and worth retrying:
// throttling/unavailability/internal-failure codes, 5xx/429 statuses (legacy
// ClientError by code, legacy ServerError and tea.SDKError with a status
// fallback), and transient network errors (timeouts, resets, broken pipes).
// Permanent errors (403/404, DNS failures, invalid parameters) are NOT
// retryable; wrapped errors are matched via errors.As.
func JudgeNeedRetry(err error) bool {
	if err == nil {
		return false
	}

	// Legacy alibaba-cloud-sdk-go error type
	var clientErr *sdkErr.ClientError
	if errors.As(err, &clientErr) {
		return isRetryableErrorCode(clientErr.ErrorCode())
	}

	// Legacy ServerError is a distinct type carrying the HTTP status, so it
	// gets the same code + 5xx/429 fallback as tea.SDKError below.
	var serverErr *sdkErr.ServerError
	if errors.As(err, &serverErr) {
		if isRetryableErrorCode(serverErr.ErrorCode()) {
			return true
		}
		status := serverErr.HttpStatus()
		return status >= http.StatusInternalServerError || status == http.StatusTooManyRequests
	}

	// New darabonba SDK error type; all fields are pointers and may be nil
	var sdkError *tea.SDKError
	if errors.As(err, &sdkError) {
		if sdkError.Code != nil && isRetryableErrorCode(*sdkError.Code) {
			return true
		}
		if sdkError.StatusCode != nil &&
			(*sdkError.StatusCode >= http.StatusInternalServerError || *sdkError.StatusCode == http.StatusTooManyRequests) {
			return true
		}
		return false
	}

	return isRetryableNetworkError(err)
}

// isRetryableNetworkError reports whether err is a network-level transient
// failure: timeouts are retryable; *net.OpError only when timeout or
// reset/broken-pipe style; DNS failures and invalid addresses are NOT.
func isRetryableNetworkError(err error) bool {
	// A non-timeout *url.Error falls through so the wrapped chain (e.g. an
	// inner *net.OpError reset) can still be classified.
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return isRetryableOpError(opErr)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

// isRetryableOpError classifies a *net.OpError. Timeouts and
// connection-reset/broken-pipe failures are transient; everything else
// (DNS resolution failures, invalid addresses, ...) is permanent.
func isRetryableOpError(opErr *net.OpError) bool {
	if opErr.Timeout() {
		return true
	}

	// Explicit DNS failures are permanent
	var dnsErr *net.DNSError
	if errors.As(opErr.Err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}

	return isConnectionInterruption(opErr.Err)
}

// isConnectionInterruption reports whether the inner OpError is a
// connection-reset/broken-pipe failure, matched portably via syscall.Errno
// (ECONNRESET/EPIPE); errno message strings are locale-dependent and avoided.
func isConnectionInterruption(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNRESET, syscall.EPIPE:
			return true
		}
	}
	return false
}

// isRetryableErrorCode reports whether the given Alibaba Cloud error code
// represents a transient failure.
func isRetryableErrorCode(code string) bool {
	switch code {
	case REJECTED_THROTTLING, SERVICE_UNAVAILABLE_TEMPORARY, INTERNAL_FAILURE:
		return true
	}
	return false
}

// RetryOnTransient invokes fn up to maxAttempts times, backing off
// exponentially (GetWaitTimeExponential) after each transient error judged
// by JudgeNeedRetry; non-retryable errors return immediately. The wait is
// interruptible via ctx: on cancellation the ctx error is wrapped (%w) with
// the last transient error included as text.
func RetryOnTransient(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts <= 0 {
		return fmt.Errorf("maxAttempts (%d) should be > 0", maxAttempts)
	}
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if !JudgeNeedRetry(err) {
			return err
		}
		if attempt == maxAttempts {
			break
		}
		wait := GetWaitTimeExponential(attempt)
		klog.Infof("transient error detected, retrying (attempt %d/%d) after %v backoff, error %v",
			attempt+1, maxAttempts, wait, err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: last transient error: %v", ctx.Err(), err)
		case <-time.After(wait):
		}
	}
	return err
}

// GetWaitTimeExponential returns 2^retryTimes * BACKOFF_DEFAULT_RETRY_INTERVAL
// with +/-20% jitter (spreads concurrent retries), capped at
// BACKOFF_DEFAULT_CAPACITY.
func GetWaitTimeExponential(retryTimes int) time.Duration {
	sleepInterval := time.Duration(math.Pow(2, float64(retryTimes))) * BACKOFF_DEFAULT_RETRY_INTERVAL
	jitter := 1.0 + (rand.Float64()*0.4 - 0.2)
	sleepInterval = time.Duration(float64(sleepInterval) * jitter)
	if sleepInterval >= BACKOFF_DEFAULT_CAPACITY {
		return BACKOFF_DEFAULT_CAPACITY
	}
	return sleepInterval
}

// IsNamespaceAllowedForClusterSecretStore reports whether the namespace may
// access the ClusterSecretStore (fail-closed on invalid selectors/regexes)
func IsNamespaceAllowedForClusterSecretStore(clusterSecretStore *v1alpha1.ClusterSecretStore, namespaceName string, getClient func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error) bool {
	// No conditions: allow all namespaces
	if len(clusterSecretStore.Spec.Conditions) == 0 {
		return true
	}

	namespace := &corev1.Namespace{}
	err := getClient(context.Background(), client.ObjectKey{Name: namespaceName}, namespace)
	if err != nil {
		klog.Errorf("Failed to get namespace %s for ClusterSecretStore %s access check: %v", namespaceName, clusterSecretStore.Name, err)
		return false
	}

	for i, condition := range clusterSecretStore.Spec.Conditions {
		klog.Infof("Evaluating condition %d for ClusterSecretStore %s", i, clusterSecretStore.Name)

		if condition.NamespaceSelector != nil {
			selector, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				// Fail-closed: an invalid selector cannot be evaluated, so deny
				// instead of silently skipping the check.
				klog.Errorf("Invalid label selector in ClusterSecretStore %s condition %d: %v", clusterSecretStore.Name, i, err)
				return false
			}

			if selector.Matches(labels.Set(namespace.Labels)) {
				return true
			}
		}

		for _, allowedNamespace := range condition.Namespaces {
			if allowedNamespace == namespaceName {
				return true
			}
		}

		// Substring match consistent with upstream ESO ClusterSecretStore
		// (regexp.MatchString). Users who need full-string matching
		// should anchor the regex themselves (e.g. "^team-a$").
		for j, regex := range condition.NamespaceRegexes {
			match, err := regexp.MatchString(regex, namespaceName)
			if err != nil {
				// Fail-closed on invalid regex
				klog.Errorf("Invalid regex %s in ClusterSecretStore %s condition %d regex %d: %v",
					regex, clusterSecretStore.Name, i, j, err)
				return false
			}

			if match {
				return true
			}
		}
	}

	klog.Warningf("Namespace %s is not allowed to access ClusterSecretStore %s", namespaceName, clusterSecretStore.Name)
	return false
}

// IsNamespaceAllowedForClusterExternalSecret reports whether the namespace is
// selected by the CES conditions (fail-closed): only an explicit match
// allows; only a fully empty selection config matches every namespace; any
// invalid selector/regex denies all namespaces.
func IsNamespaceAllowedForClusterExternalSecret(ces *v1alpha1.ClusterExternalSecret, namespace corev1.Namespace) bool {
	// Empty selection config (no legacy selectors AND no conditions) matches all
	if len(ces.Spec.NamespaceSelectors) == 0 && len(ces.Spec.Conditions) == 0 {
		return true
	}

	for i, selector := range ces.Spec.NamespaceSelectors {
		if selector == nil {
			continue
		}
		namespaceSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			// Fail-closed: an invalid selector cannot be evaluated, so deny
			// instead of silently skipping the check.
			klog.Errorf("Invalid label selector in ClusterExternalSecret %s namespace selector %d: %v", ces.Name, i, err)
			return false
		}

		if namespaceSelector.Matches(labels.Set(namespace.Labels)) {
			return true
		}
	}

	for i, condition := range ces.Spec.Conditions {
		klog.Infof("Evaluating condition %d for ClusterExternalSecret %s", i, ces.Name)

		if condition.NamespaceSelector != nil {
			selector, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				// Fail-closed on invalid selector (see above)
				klog.Errorf("Invalid label selector in ClusterExternalSecret %s condition %d: %v", ces.Name, i, err)
				return false
			}

			if selector.Matches(labels.Set(namespace.Labels)) {
				return true
			}
		}

		for _, allowedNamespace := range condition.Namespaces {
			if allowedNamespace == namespace.Name {
				return true
			}
		}

		// Substring match consistent with upstream ESO ClusterSecretStore
		// (regexp.MatchString). Users who need full-string matching
		// should anchor the regex themselves (e.g. "^team-a$").
		for j, regex := range condition.NamespaceRegexes {
			match, err := regexp.MatchString(regex, namespace.Name)
			if err != nil {
				// Fail-closed on invalid regex
				klog.Errorf("Invalid regex %s in ClusterExternalSecret %s condition %d regex %d: %v",
					regex, ces.Name, i, j, err)
				return false
			}

			if match {
				return true
			}
		}
	}

	klog.Warningf("Namespace %s is not allowed to access ClusterExternalSecret %s", namespace.Name, ces.Name)
	return false
}

func GenerateDefaultOidcProviderArn(clusterId string, uid int64) string {
	name := fmt.Sprintf(oidcProviderNameTemplate, clusterId)
	return fmt.Sprintf("acs:ram::%d:oidc-provider/%s", uid, name)
}

func IsValidOidcProviderArn(arn string) bool {
	return reOidcProviderArn.Match([]byte(arn))
}
