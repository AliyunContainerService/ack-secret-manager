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
	"regexp"
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
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	BinaryType               = "binary"
	oidcProviderNameTemplate = "ack-rrsa-%s"
)

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

var clusterIDPattern = regexp.MustCompile(`^c[0-9a-z]{32}$`)

func IsClusterNamespace(s string) bool {
	return clusterIDPattern.MatchString(s)
}

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Remove returns a new slice with all occurrences of s removed. The input
// slice is never modified in place, and adjacent duplicate occurrences are
// all removed (the previous in-place single-pass implementation skipped the
// element that shifted into the removed slot).
func Remove(list []string, s string) []string {
	result := make([]string, 0, len(list))
	for _, v := range list {
		if v != s {
			result = append(result, v)
		}
	}
	return result
}

// getKubernetesClients returns all the required clients(token CRD client and origin k8s cli) to communicate with
func GetKubernetesClients() (dynamic.Interface, error) {
	var err error
	var cfg *rest.Config

	cfg, err = rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("error loading kubernetes configuration inside cluster, "+
			"check app is running outside kubernetes cluster or run in development mode: %s", err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
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

func JsonStr(o interface{}) string {
	temp, ok := o.(string)
	if ok {
		return temp
	}
	str, _ := json.Marshal(o)
	return string(str)
}

func YamlStr(o interface{}) string {
	temp, ok := o.(string)
	if ok {
		return temp
	}
	str, _ := yaml.Marshal(o)
	return string(str)
}

// Ignore not found errors
func IgnoreNotFoundError(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// IsValidJSON reports whether the given byte slice is a valid JSON document.
// The check is intentionally strict: the raw first byte must be '{' or '['
// (no whitespace trimming, since a leading-whitespace JSON is treated as YAML
// by design), and the content must parse with encoding/json. Empty input is
// rejected.
func IsValidJSON(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// Quick check: JSON must start with { or [
	firstChar := data[0]
	if firstChar != '{' && firstChar != '[' {
		return false
	}
	// Attempt to unmarshal as JSON
	var js interface{}
	return json.Unmarshal(data, &js) == nil
}

// GetJsonSecrets evaluates the configured jmesPath expressions against the
// fetched secret value (JSON or YAML). Per-expression failures are logged
// (always including the source secret key) and skipped; the only returned
// error is a structurally invalid (non-JSON/YAML) secret value.
func GetJsonSecrets(jmesObj []v1alpha1.JMESPathObject, secretValue, key string) (jsonMap map[string]string, err error) {
	jsonMap = make(map[string]string, 0)
	var data interface{}
	// Attempt to unmarshal the secretValue as YAML. If it fails, try to unmarshal it as JSON.
	// If both attempts fail, return an error indicating that the provided value is neither valid JSON nor YAML.
	//
	// The output format of complex (map/slice) values is decided by the input
	// format, not by the parser that succeeded (JSON is a YAML subset, so a
	// YAML parse of a JSON document always succeeds): a strict-JSON input must
	// keep emitting compact JSON, while YAML input keeps emitting YAML.
	marshalToYaml := !IsValidJSON([]byte(secretValue))
	if err = yaml.Unmarshal([]byte(secretValue), &data); err != nil {
		if err = json.Unmarshal([]byte(secretValue), &data); err != nil {
			return nil, fmt.Errorf("invalid JSON or YAML used with jmesPath in secret key: %s", key)
		}
	}

	//fetch all specified key value pairs`
	for _, jmesPathEntry := range jmesObj {
		jsonSecret, err := jmespath.Search(jmesPathEntry.Path, data)
		if err != nil {
			klog.Errorf("invalid JMES Path: %s, secret key: %s, err = %s", jmesPathEntry.Path, key, err.Error())
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
					klog.Errorf("failed to marshal jmesPath value to YAML, secret key: %s, object alias: %v, type: %T, error: %v", key, jmesPathEntry.ObjectAlias, v, err)
					continue
				}
				strValue = string(yamlData)
			} else {
				jsonData, err := json.Marshal(v)
				if err != nil {
					klog.Errorf("failed to marshal jmesPath value to JSON, secret key: %s, object alias: %v, type: %T, error: %v", key, jmesPathEntry.ObjectAlias, v, err)
					continue
				}
				strValue = string(jsonData)
			}

		default:
			klog.Errorf("unsupported jmesPath value type, secret key: %s, object alias: %v, type: %T", key, jmesPathEntry.ObjectAlias, v)
			continue
		}

		// An empty ObjectAlias would otherwise produce an illegal empty Secret
		// key that the API server rejects atomically (failing the whole write
		// and every subsequent retry). Fall back to the source data.Key,
		// mirroring ResolveTargetKey's empty-Name fallback.
		targetKey := jmesPathEntry.ObjectAlias
		if targetKey == "" {
			targetKey = key
		}
		jsonMap[targetKey] = strValue
	}

	return jsonMap, nil
}

// RewriteRegexp rewrites a single Regexp Rewrite Operation. An
// uncompilable Source regexp is a configuration error: it is returned
// wrapped with the offending pattern so the caller can fail the sync
// instead of silently discarding the extracted keys.
func RewriteRegexp(operation v1alpha1.ReplaceRule, in map[string]string) (map[string]string, error) {
	out := make(map[string]string)
	re, err := regexp.Compile(operation.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to compile replaceKey regexp %q: %v", operation.Source, err)
	}
	for key, value := range in {
		newKey := re.ReplaceAllString(key, operation.Target)
		if _, exists := out[newKey]; exists {
			// Multiple source keys collapsed onto the same rewritten key:
			// keep the overwrite semantics but surface the data loss.
			klog.Warningf("key conflict after regex rewrite: source %q and a previous key both rewrite to %q (source pattern %q), overwriting",
				key, newKey, operation.Source)
		}
		out[newKey] = value
	}
	return out, nil
}

// JudgeNeedRetry reports whether the given error is transient and worth
// retrying. It recognizes:
//   - the legacy alibaba-cloud-sdk-go ClientError and the new darabonba
//     tea.SDKError used by the kms-20160120/v3 and oos-20190601/v3 clients,
//     even when wrapped via fmt.Errorf("%w") (matched via errors.As);
//   - network-level transient errors: request timeouts surfaced as *url.Error
//     or net.Error (judged by Timeout()), and socket-level failures such as
//     connection resets or broken pipes surfaced as *net.OpError. Permanent
//     network failures such as DNS resolution errors are NOT retryable.
//
// Transient errors include throttling (Rejected.Throttling), temporary
// service unavailability (ServiceUnavailableTemporary), internal failures
// (InternalFailure), any 5xx or 429 HTTP status (darabonba tea.SDKError
// status fallback only; the legacy ClientError branch judges by error code)
// and the network errors above. Permanent errors such as 403/404 or
// InvalidParameter are NOT retryable.
func JudgeNeedRetry(err error) bool {
	if err == nil {
		return false
	}

	// Legacy alibaba-cloud-sdk-go error type. errors.As is used so errors
	// wrapped with fmt.Errorf("%w") are also recognized.
	var clientErr *sdkErr.ClientError
	if errors.As(err, &clientErr) {
		return isRetryableErrorCode(clientErr.ErrorCode())
	}

	// New darabonba SDK error type. All fields are pointers and may be nil.
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

// isRetryableNetworkError reports whether the given error is a network-level
// transient failure. Timeout errors (matched via *url.Error or net.Error and
// judged by Timeout()) are retryable. Socket-level errors (*net.OpError) are
// retried only when they are timeouts or connection-reset/broken-pipe style
// failures; permanent failures such as DNS resolution errors or invalid
// addresses (e.g. a misconfigured kmsEndpoint) are NOT retried.
func isRetryableNetworkError(err error) bool {
	// A *url.Error carries no socket-level detail itself; when it is not a
	// timeout, fall through so the wrapped error chain (e.g. an inner
	// *net.OpError connection reset) can still be classified.
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

	// Explicit DNS failures are permanent: retrying "no such host" cannot
	// succeed within a reconcile round.
	var dnsErr *net.DNSError
	if errors.As(opErr.Err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}

	return isConnectionInterruption(opErr.Err)
}

// isConnectionInterruption reports whether the inner error of an OpError is a
// connection-reset or broken-pipe style failure. It first matches the
// well-known errno values via syscall.Errno -- ECONNRESET and EPIPE are
// defined by Go's syscall package on every supported platform (on Windows
// they map to WSAECONNRESET / WSAEPIPE), so this is the preferred portable
// check. Matching on the literal error string is deliberately avoided because
// errno message text is platform- and locale-dependent.
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

// RetryOnTransient invokes fn up to maxAttempts times in total. After each
// failed attempt, if the error is judged transient by JudgeNeedRetry, it
// backs off exponentially via GetWaitTimeExponential before retrying.
// Non-retryable errors are returned immediately without further attempts.
// If all attempts fail, the last error is returned. The backoff wait is
// interruptible via ctx: if ctx is cancelled while waiting, the cancellation
// error is returned via fmt.Errorf %w with the last transient error included
// as text (only the cancellation error is wrapped), so the root cause is not
// lost.
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
		klog.Warningf("transient error detected, retrying (attempt %d/%d) after %v backoff, error %v",
			attempt+1, maxAttempts, wait, err)
		select {
		case <-ctx.Done():
			// The cancellation error is wrapped via %w; the last transient
			// error is included as text so callers can see what was being
			// retried.
			return fmt.Errorf("%w: last transient error: %v", ctx.Err(), err)
		case <-time.After(wait):
		}
	}
	return err
}

// GetWaitTimeExponential returns the backoff duration for the given retry
// round: 2^retryTimes * BACKOFF_DEFAULT_RETRY_INTERVAL with a +/-20% random
// jitter applied, capped at BACKOFF_DEFAULT_CAPACITY. The jitter spreads
// concurrent retries over time to avoid synchronized retry storms. The global
// math/rand source is automatically seeded since Go 1.20.
func GetWaitTimeExponential(retryTimes int) time.Duration {
	sleepInterval := time.Duration(math.Pow(2, float64(retryTimes))) * BACKOFF_DEFAULT_RETRY_INTERVAL
	// Jitter factor in [0.8, 1.2).
	jitter := 1.0 + (rand.Float64()*0.4 - 0.2)
	sleepInterval = time.Duration(float64(sleepInterval) * jitter)
	if sleepInterval >= BACKOFF_DEFAULT_CAPACITY {
		return BACKOFF_DEFAULT_CAPACITY
	}
	return sleepInterval
}

// IsNamespaceAllowedForClusterSecretStore checks if the given namespace is allowed to access the ClusterSecretStore
func IsNamespaceAllowedForClusterSecretStore(clusterSecretStore *v1alpha1.ClusterSecretStore, namespaceName string, getClient func(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error) bool {
	// If no conditions are specified, allow access from all namespaces
	if len(clusterSecretStore.Spec.Conditions) == 0 {
		return true
	}

	// Get namespace object
	namespace := &corev1.Namespace{}
	err := getClient(context.Background(), client.ObjectKey{Name: namespaceName}, namespace)
	if err != nil {
		klog.Errorf("Failed to get namespace %s for ClusterSecretStore %s access check: %v", namespaceName, clusterSecretStore.Name, err)
		return false
	}

	// Check each condition
	for i, condition := range clusterSecretStore.Spec.Conditions {
		klog.Infof("Evaluating condition %d for ClusterSecretStore %s", i, clusterSecretStore.Name)

		// Check namespace selector
		if condition.NamespaceSelector != nil {
			selector, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				// Fail-closed: an invalid label selector means the restriction
				// cannot be evaluated, so we deny access rather than silently
				// skipping the check (aligned with the invalid-regex path).
				klog.Errorf("Invalid label selector in ClusterSecretStore %s condition %d: %v", clusterSecretStore.Name, i, err)
				return false
			}

			if selector.Matches(labels.Set(namespace.Labels)) {
				return true
			}
		}

		// Check namespace name list
		for _, allowedNamespace := range condition.Namespaces {
			if allowedNamespace == namespaceName {
				return true
			}
		}

		// Check namespace regex (anchored full-string match to prevent substring bypass)
		for j, regex := range condition.NamespaceRegexes {
			// Anchor the pattern to match the entire namespace name, not a substring.
			// This prevents e.g. regex "team-a" from matching "evil-team-a".
			anchoredRegex := "^(?:" + regex + ")$"
			re, err := regexp.Compile(anchoredRegex)
			if err != nil {
				// Fail-closed: invalid regex means the restriction cannot be evaluated,
				// so we deny access rather than silently skipping the check.
				klog.Errorf("Invalid regex %s in ClusterSecretStore %s condition %d regex %d: %v",
					regex, clusterSecretStore.Name, i, j, err)
				return false
			}

			if re.MatchString(namespaceName) {
				return true
			}
		}
	}

	// No matching condition
	klog.Infof("Namespace %s is not allowed to access ClusterSecretStore %s", namespaceName, clusterSecretStore.Name)
	return false
}

// IsNamespaceAllowedForClusterExternalSecret checks if the given namespace is
// allowed by the ClusterExternalSecret conditions. It is fail-closed: a
// namespace is only allowed when it explicitly matches a configured
// namespaceSelector / condition. Only when neither namespaceSelectors nor
// conditions are configured at all does the resource match every namespace.
// Any invalid matching configuration (unparseable label selector, invalid
// regex) denies every namespace immediately, aligned with
// IsNamespaceAllowedForClusterSecretStore.
func IsNamespaceAllowedForClusterExternalSecret(ces *v1alpha1.ClusterExternalSecret, namespace corev1.Namespace) bool {
	// Only an empty selection config (no legacy namespaceSelectors AND no
	// conditions) allows all namespaces; otherwise a namespace must
	// explicitly match.
	if len(ces.Spec.NamespaceSelectors) == 0 && len(ces.Spec.Conditions) == 0 {
		return true
	}

	// Check each legacy namespace selector
	for i, selector := range ces.Spec.NamespaceSelectors {
		if selector == nil {
			continue
		}
		namespaceSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			// Fail-closed: an invalid label selector means the restriction
			// cannot be evaluated, so we deny access rather than silently
			// skipping the check (aligned with
			// IsNamespaceAllowedForClusterSecretStore).
			klog.Errorf("Invalid label selector in ClusterExternalSecret %s namespace selector %d: %v", ces.Name, i, err)
			return false
		}

		if namespaceSelector.Matches(labels.Set(namespace.Labels)) {
			return true
		}
	}

	// Check each condition
	for i, condition := range ces.Spec.Conditions {
		klog.Infof("Evaluating condition %d for ClusterExternalSecret %s", i, ces.Name)

		// Check namespace selector
		if condition.NamespaceSelector != nil {
			selector, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				// Fail-closed: an invalid label selector means the restriction
				// cannot be evaluated, so we deny access rather than silently
				// skipping the check (aligned with
				// IsNamespaceAllowedForClusterSecretStore).
				klog.Errorf("Invalid label selector in ClusterExternalSecret %s condition %d: %v", ces.Name, i, err)
				return false
			}

			if selector.Matches(labels.Set(namespace.Labels)) {
				return true
			}
		}

		// Check namespace name list
		for _, allowedNamespace := range condition.Namespaces {
			if allowedNamespace == namespace.Name {
				return true
			}
		}

		// Check namespace regex (anchored full-string match to prevent substring bypass)
		for j, regex := range condition.NamespaceRegexes {
			// Anchor the pattern to match the entire namespace name, not a substring.
			anchoredRegex := "^(?:" + regex + ")$"
			re, err := regexp.Compile(anchoredRegex)
			if err != nil {
				// Fail-closed: invalid regex means the restriction cannot be evaluated,
				// so we deny access rather than silently skipping the check.
				klog.Errorf("Invalid regex %s in ClusterExternalSecret %s condition %d regex %d: %v",
					regex, ces.Name, i, j, err)
				return false
			}

			if re.MatchString(namespace.Name) {
				return true
			}
		}
	}

	// No matching condition
	klog.Infof("Namespace %s is not allowed to access ClusterExternalSecret %s", namespace.Name, ces.Name)
	return false
}

func GenerateDefaultOidcProviderArn(clusterId string, uid int64) string {
	name := fmt.Sprintf(oidcProviderNameTemplate, clusterId)
	return fmt.Sprintf("acs:ram::%d:oidc-provider/%s", uid, name)
}

func IsValidOidcProviderArn(arn string) bool {
	return reOidcProviderArn.Match([]byte(arn))
}
