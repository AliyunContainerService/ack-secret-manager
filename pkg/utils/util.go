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
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/jmespath/go-jmespath"
	"k8s.io/klog"
)

const (
	BinaryType   = "binary"
	METADATA_URL = "http://100.100.100.200/latest/meta-data/"
	REGIONID_TAG = "region-id"
	RAM          = "ram/"
)

const (
	REJECTED_THROTTLING           = "Rejected.Throttling"
	SERVICE_UNAVAILABLE_TEMPORARY = "ServiceUnavailableTemporary"
	INTERNAL_FAILURE              = "InternalFailure"
)

var (
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Second
	BACKOFF_DEFAULT_CAPACITY       = time.Duration(10) * time.Second
)

var clusterIDPattern = regexp.MustCompile(`^c[0-9a-z]{32}$`)

func IsClusterNamespace(s string) bool {
	return clusterIDPattern.MatchString(s)
}

type ConditionFunc func() (bool, error)

// Retry retries f every interval until after maxRetries.
// The interval won't be affected by how long f takes.
// For example, if interval is 3s, f takes 1s, another f will be called 2s later.
// However, if f takes longer than interval, it will be delayed.
func Retry(interval time.Duration, maxRetries int, f ConditionFunc) error {
	if maxRetries <= 0 {
		return fmt.Errorf("maxRetries (%d) should be > 0", maxRetries)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for i := 0; ; i++ {
		ok, err := f()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if i == maxRetries {
			break
		}
		<-tick.C
	}
	return fmt.Errorf("still failing after %d retries", maxRetries)
}

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func Remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
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

// GetMetaData get metadata
func GetMetaData(resource string) (int, string, error) {
	resp, err := http.Get(METADATA_URL + resource)
	if err != nil {
		return http.StatusInternalServerError, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), err
}

// GetRegion Get regionid
func GetRegion() (string, error) {
	_, regionId, err := GetMetaData(REGIONID_TAG)
	if err != nil {
		return "", err
	}
	return regionId, nil
}

// CheckInstanceRam check if instance not bind workerrole
func CheckInstanceRam() (bool, error) {
	status, _, err := GetMetaData(RAM)
	if err != nil {
		return false, fmt.Errorf("received %d getting instance role", status)
	}
	return true, nil
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

func GetJsonSecrets(jmesObj []v1alpha1.JMESPathObject, secretValue, key string) (jsonMap map[string]string, err error) {
	jsonMap = make(map[string]string, 0)
	var data interface{}
	// Attempt to unmarshal the secretValue as YAML. If it fails, try to unmarshal it as JSON.
	// If both attempts fail, return an error indicating that the provided value is neither valid JSON nor YAML.
	marshalToYaml := true
	if err = yaml.Unmarshal([]byte(secretValue), &data); err != nil {
		marshalToYaml = false
		if err = json.Unmarshal([]byte(secretValue), &data); err != nil {
			return nil, fmt.Errorf("invalid JSON or YAML used with jmesPath in secret key: %s", key)
		}
	}

	//fetch all specified key value pairs`
	for _, jmesPathEntry := range jmesObj {
		jsonSecret, err := jmespath.Search(jmesPathEntry.Path, data)
		if err != nil {
			klog.Errorf("Invalid JMES Path: %s.", jmesPathEntry.Path)
			continue
		}

		if jsonSecret == nil {
			klog.Errorf("JMES Path - %s for object alias - %s does not point to a valid object.",
				jmesPathEntry.Path, jmesPathEntry.ObjectAlias)
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
					klog.Errorf("failed to marshal value to JSON, key: %v, type: %T, error: %v", jmesPathEntry.ObjectAlias, v, err)
					continue
				}
				strValue = string(yamlData)
			} else {
				jsonData, err := json.Marshal(v)
				if err != nil {
					klog.Errorf("failed to marshal value to JSON, key: %v, type: %T, error: %v", jmesPathEntry.ObjectAlias, v, err)
					continue
				}
				strValue = string(jsonData)
			}

		default:
			klog.Errorf("unsupported value type for key: %v, type: %T", jmesPathEntry.ObjectAlias, v)
			continue
		}

		jsonMap[jmesPathEntry.ObjectAlias] = strValue
	}

	return jsonMap, nil
}

// RewriteRegexp rewrites a single Regexp Rewrite Operation.
func RewriteRegexp(operation v1alpha1.ReplaceRule, in map[string]string) (map[string]string, error) {
	out := make(map[string]string)
	re, err := regexp.Compile(operation.Source)
	if err != nil {
		return nil, err
	}
	for key, value := range in {
		newKey := re.ReplaceAllString(key, operation.Target)
		out[newKey] = value
	}
	return out, nil
}

func JudgeNeedRetry(err error) bool {
	respErr, is := err.(*sdkErr.ClientError)
	if is && (respErr.ErrorCode() == REJECTED_THROTTLING || respErr.ErrorCode() == SERVICE_UNAVAILABLE_TEMPORARY || respErr.ErrorCode() == INTERNAL_FAILURE) {
		return true
	}
	return false
}

func GetWaitTimeExponential(retryTimes int) time.Duration {
	sleepInterval := time.Duration(math.Pow(2, float64(retryTimes))) * BACKOFF_DEFAULT_RETRY_INTERVAL
	if sleepInterval >= BACKOFF_DEFAULT_CAPACITY {
		return BACKOFF_DEFAULT_CAPACITY
	} else {
		return sleepInterval
	}
}
