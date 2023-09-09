package externalsecret

import (
	"encoding/json"
	"fmt"
	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/jmespath/go-jmespath"
)

func getJsonSecrets(jmesObj []api.JMESPathObject, secretValue, key string) (jsonMap map[string]string, err error) {
	jsonMap = make(map[string]string, 0)
	var data interface{}
	err = json.Unmarshal([]byte(secretValue), &data)
	if err != nil {
		return nil, fmt.Errorf("Invalid JSON used with jmesPath in secret key: %s.", key)
	}
	//fetch all specified key value pairs`
	for _, jmesPathEntry := range jmesObj {
		jsonSecret, err := jmespath.Search(jmesPathEntry.Path, data)
		if err != nil {
			return nil, fmt.Errorf("Invalid JMES Path: %s.", jmesPathEntry.Path)
		}

		if jsonSecret == nil {
			return nil, fmt.Errorf("JMES Path - %s for object alias - %s does not point to a valid object.",
				jmesPathEntry.Path, jmesPathEntry.ObjectAlias)
		}

		jsonSecretAsString, isString := jsonSecret.(string)
		if !isString {
			return nil, fmt.Errorf("Invalid JMES search result type for path:%s. Only string is allowed.", jmesPathEntry.Path)
		}
		jsonMap[jmesPathEntry.ObjectAlias] = jsonSecretAsString
	}
	return jsonMap, nil
}
