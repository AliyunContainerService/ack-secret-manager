package externalsecret

import "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/apis/alibabacloud/v1alpha1"

var (
	data                    = make([]v1alpha1.DataSource, 0)
	process                 = make([]v1alpha1.DataProcess, 0)
	externalSecretName      string
	externalSecretNamespace string
	secretType              string
)
