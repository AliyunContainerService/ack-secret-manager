module github.com/AliyunContainerService/ack-secret-manager

go 1.12

require (
	github.com/alibabacloud-go/darabonba-openapi v0.1.4
	github.com/alibabacloud-go/kms-20160120/v2 v2.0.0
	github.com/alibabacloud-go/tea v1.1.15
	github.com/aliyun/alibaba-cloud-sdk-go v1.61.127
	github.com/aliyun/credentials-go v1.2.2
	github.com/go-logr/logr v0.3.0
	github.com/go-openapi/spec v0.19.4 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/jmespath/go-jmespath v0.0.0-20180206201540-c2b33e8439af
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/operator-framework/operator-lib v0.4.1
	golang.org/x/net v0.0.0-20201110031124-69a78807bb2b
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/code-generator v0.20.1
	sigs.k8s.io/controller-runtime v0.8.3
)

replace k8s.io/client-go => k8s.io/client-go v0.20.2
