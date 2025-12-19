package common

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
	"k8s.io/klog"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// ProcessExternalSecretData processes external secret data with JMESPath support
func ProcessExternalSecretData(data *v1alpha1.DataSource, externalData []byte) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)

	// Process JMESPath expressions if present
	if len(data.JMESPath) > 0 {
		klog.Infof("parse jmes format, key %v", data.Key)
		jsonDataMap, err := utils.GetJsonSecrets(data.JMESPath, string(externalData), data.Key)
		if err != nil {
			klog.Errorf("parse jmes format error %v, key %v, jmes %v", err, data.Key, data.JMESPath)
		} else if len(jsonDataMap) > 0 {
			// Use parsed k-value in target secret
			for k, v := range jsonDataMap {
				secretDatas[k] = []byte(v)
			}
			return secretDatas, nil
		}
	}

	// Use the default name if no JMESPath processing was done
	secretDatas[data.Name] = externalData
	return secretDatas, nil
}

// ProcessExtractedExternalSecretData processes extracted secret data from YAML/JSON
func ProcessExtractedExternalSecretData(data *v1alpha1.DataProcess, externalData []byte) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)

	tempKV := make(map[string]interface{})
	marshalToYaml := true

	// Attempt to parse the external data as YAML. If parsing fails, try parsing it as JSON.
	// If both parsing attempts fail, log an error and return the error.
	if err := yaml.Unmarshal(externalData, &tempKV); err != nil {
		marshalToYaml = false
		if err := json.Unmarshal(externalData, &tempKV); err != nil {
			klog.Errorf("extract secret error %v key %v", err, data.Extract.Key)
			return nil, err
		}
	}

	kv := make(map[string]string)
	for k, v := range tempKV {
		if marshalToYaml {
			kv[k] = utils.YamlStr(v)
		} else {
			kv[k] = utils.JsonStr(v)
		}
	}

	// Apply key replacement rules if any
	if len(data.ReplaceKey) != 0 {
		for _, rule := range data.ReplaceKey {
			var err error
			kv, err = utils.RewriteRegexp(rule, kv)
			if err != nil {
				klog.Errorf("replace data key failed, error %v", err)
				continue
			}
		}
	}

	// Convert to final result map
	for k, v := range kv {
		secretDatas[k] = []byte(v)
	}

	return secretDatas, nil
}
