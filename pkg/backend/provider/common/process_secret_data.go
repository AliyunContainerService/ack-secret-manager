package common

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
	"k8s.io/klog"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// isValidJSON delegates to utils.IsValidJSON.
func isValidJSON(data []byte) bool {
	return utils.IsValidJSON(data)
}

// ResolveTargetKey returns the target key for a non-jmesPath data entry:
// spec.data[].name when set, otherwise spec.data[].key. Single
// key-derivation implementation shared by the success and failure paths.
func ResolveTargetKey(data *v1alpha1.DataSource) string {
	if data.Name != "" {
		return data.Name
	}
	return data.Key
}

// ProcessExternalSecretData processes external secret data with JMESPath support
func ProcessExternalSecretData(data *v1alpha1.DataSource, externalData []byte) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)

	if len(data.JMESPath) > 0 {
		klog.Infof("parse jmes format, key %v", data.Key)
		jsonDataMap, err := utils.GetJsonSecrets(data.JMESPath, string(externalData), data.Key)
		if err != nil {
			// Single log point for the parse failure plus its consequence:
			// the entry falls through to the raw-data fallback below.
			klog.Errorf("parse jmes format error %v, key %v, jmes %v; falling back to raw data", err, data.Key, data.JMESPath)
		} else if len(jsonDataMap) > 0 {
			for k, v := range jsonDataMap {
				secretDatas[k] = []byte(v)
			}
			return secretDatas, nil
		}
	}

	// Fallback key: name, or data.Key when name is omitted, so the Secret
	// entry never carries an empty key (rejected by the API server).
	secretDatas[ResolveTargetKey(data)] = externalData
	return secretDatas, nil
}

// ProcessExtractedExternalSecretData processes extracted secret data from
// YAML/JSON, preserving the original format (JSON in -> JSON out).
func ProcessExtractedExternalSecretData(data *v1alpha1.DataProcess, externalData []byte) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)

	tempKV := make(map[string]interface{})

	// JSON is stricter, so validate it before attempting YAML
	isJSON := isValidJSON(externalData)

	var err error
	if isJSON {
		err = json.Unmarshal(externalData, &tempKV)
	} else {
		err = yaml.Unmarshal(externalData, &tempKV)
	}

	if err != nil {
		klog.Errorf("extract secret error: failed to parse data, key %v, err %v", data.Extract.Key, err)
		return nil, err
	}

	// Serialize in the input format
	kv := make(map[string]string)
	for k, v := range tempKV {
		if isJSON {
			kv[k] = utils.JsonStr(v)
		} else {
			kv[k] = utils.YamlStr(v)
		}
	}

	// Apply key replacement rules. A rule that fails to compile is a
	// configuration error: fail closed instead of continuing, because a nil
	// rewrite result would silently drop every extracted key.
	if len(data.ReplaceKey) != 0 {
		for _, rule := range data.ReplaceKey {
			var err error
			kv, err = utils.RewriteRegexp(rule, kv)
			if err != nil {
				klog.Errorf("replace data key failed, key %v, error %v", data.Extract.Key, err)
				return nil, err
			}
		}
	}

	for k, v := range kv {
		secretDatas[k] = []byte(v)
	}

	return secretDatas, nil
}
