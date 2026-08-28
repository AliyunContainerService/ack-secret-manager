// resource_manager_test.go - Cloud-resource test infrastructure for ACK Secret Manager E2E tests.
//
// This file contains NO Ginkgo Describe/It blocks. It provides helper types
// (GlobalResourceManager, ResourceManager) and functions that manage
// Alibaba Cloud resources (KMS secrets, OOS parameters, RAM roles/policies,
// ACK cluster node-pool labels) used as fixtures by the spec files. It is
// compiled as part of the e2e test binary (package e2e, *_test.go naming
// convention) so that these helpers remain unexported but accessible to all
// spec files in the package.
//
// See test/e2e/README.md for the distinction between infrastructure files
// (suite_test.go, resource_manager_test.go, helpers_test.go) and spec files.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	cs20151215 "github.com/alibabacloud-go/cs-20151215/v7/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	kms20160120 "github.com/alibabacloud-go/kms-20160120/v3/client"
	oos20190601 "github.com/alibabacloud-go/oos-20190601/v4/client"
	ram20150501 "github.com/alibabacloud-go/ram-20150501/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestResourcePrefix          = "acksm-test-"
	PolicyType                  = "Custom"
	ServiceaccountNameForSAAuth = "test-serviceaccount-auth"
	ACKRRSAAnnotation           = "ack.alibabacloud.com/role-arn"

	// oosWatchParamNamePrefix is the naming convention of the dedicated OOS
	// secret parameters created by CreateOOSSecretParameterForUpdate; the
	// AfterSuite fallback sweep deletes any leftover entry matching it.
	oosWatchParamNamePrefix = TestResourcePrefix + "oos-watch-param-"
)

// Preset credential values created by ResourceManager. They are exported so
// e2e assertions can verify that synced K8s Secret contents exactly match the
// source KMS/OOS values instead of only checking for non-empty data.
const (
	CommonKMSSecretValue          = "this ia a common kms secret for testing"
	CommonOOSSecretParameterValue = "this is an oos encrypted parameter for testing"
)

// CredentialUpdateKMSSecretInitialValue is the v1 seed value written by
// CreateKMSSecretForCredentialUpdate. Shared so specs asserting on the
// initial value of a dedicated credential-update KMS secret (e.g. the
// --disable-polling Consistently leg) reference one source of truth instead
// of duplicating the literal.
const CredentialUpdateKMSSecretInitialValue = "initial kms secret data for reconcile update testing"

const (
	CSEndpointFormat  = "cs.%s.aliyuncs.com"
	OOSEndpointFormat = "oos.%s.aliyuncs.com"
	RAMEndpointFormat = "ram.aliyuncs.com"
)

const (
	DedicatedKMSEndpointFormat = "%s.cryptoservice.kms.aliyuncs.com"
	SharedKMSEndpointFormat    = "kms.%s.aliyuncs.com"
)

// Global variables to test resources reference
var (
	GlobalResourceManager             *ResourceManager
	CommonKMSSecretName               string
	JsonKMSSecretName                 string
	YamlKMSSecretName                 string
	DedicatedKMSEndpoint              string
	SharedKMSEndpoint                 string
	CommonOOSSecretParameterName      string
	RAMUserAccessKeyID                string
	RAMUserAccessKeySecret            string
	RAMRoleArnForRRSA                 string
	OIDCProviderARN                   string
	RAMRoleArnForRolePlay             string
	RAMUserAccessKeyIDForRolePlay     string
	RAMUserAccessKeySecretForRolePlay string
	RAMRoleArnForSAAuth               string
	ServiceaccountNamespaceForSAAuth  *corev1.Namespace

	// Cross-account test variables (only populated when CROSS_ACCOUNT_* account
	// credentials are configured; otherwise left empty so cross-account
	// tests hit their Skip conditions)
	RAMRoleArnForCrossAccount string
	CrossAccountKMSSecretName string // KMS secret name created in the target account for cross-account testing

	// Template test specific variables
	SimpleTemplateSecretName    string
	GoTemplateSecretName        string
	AuthCredentialsSecretName   string
	MergeJsonBaseSecretName     string
	MergeJsonOverrideSecretName string

	// Advanced template test specific variables
	AdvancedServiceMetadataSecret    string
	AdvancedEnvironmentConfigSecret  string
	AdvancedDatabaseCredsSecret      string
	AdvancedRedisConfigSecret        string
	AdvancedLoggingConfigSecret      string
	AdvancedAppNameSecret            string
	AdvancedAppVersionSecret         string
	AdvancedReplicasSecret           string
	AdvancedImageRepoSecret          string
	AdvancedImageTagSecret           string
	AdvancedPortsSecret              string
	AdvancedPrivateKeySecret         string
	AdvancedCertificateSecret        string
	AdvancedCABundleSecret           string
	AdvancedKeystorePasswordSecret   string
	AdvancedCurrentEnvironmentSecret string
	AdvancedServiceNameSecret        string
)

// Global variables to test resources name
var (
	ClusterWorkerRoleName      string
	ClusterVPCID               string
	ClusterVSwitchID           string
	PolicyNameForAccess        string
	PolicyNameForRolePlay      string
	RAMUserNameForAccessKey    string
	RAMRoleNameForRRSA         string
	RAMUserNameForRolePlay     string
	RAMRoleNameForRolePlay     string
	RAMRoleNameForSAAuth       string
	RAMRoleNameForCrossAccount string
	CrossAccountPolicyName     string // KMS access policy created for the cross-account role in the remote target account
)

// ResourceManager manages cloud resources needed for E2E tests
type ResourceManager struct {
	kmsClient           *kms20160120.Client
	ramClient           *ram20150501.Client
	oosClient           *oos20190601.Client
	csClient            *cs20151215.Client
	accountID           string
	clusterID           string
	regionID            string
	commonKMSInstanceID string
	commonKMSKeyID      string

	// Remote account clients for cross-account testing
	// When remote account AK/SK is configured, these clients use the target account's credentials
	// to create resources in the target account (simulating true cross-account scenario)
	remoteAccountID     string
	remoteRamClient     *ram20150501.Client
	remoteKmsClient     *kms20160120.Client
	remoteKMSKeyID      string // KMS key ID in the target account
	remoteKMSInstanceID string // KMS instance ID in the target account
}

// NewResourceManager creates a new ResourceManager instance
func NewResourceManager(accountID, clusterID string) (*ResourceManager, error) {
	region := os.Getenv("REGION")
	if region == "" {
		return nil, fmt.Errorf("required environment variable REGION is not set; REGION is required to build the KMS/OOS/CS service endpoints")
	}

	credential, err := credential.NewCredential(nil)
	if err != nil {
		return nil, err
	}

	config := &openapi.Config{
		Credential: credential,
	}
	config.Endpoint = tea.String(fmt.Sprintf(SharedKMSEndpointFormat, region))
	kmsClient, err := kms20160120.NewClient(config)
	if err != nil {
		return nil, err
	}

	config.Endpoint = tea.String(RAMEndpointFormat)
	ramClient, err := ram20150501.NewClient(config)
	if err != nil {
		return nil, err
	}

	config.Endpoint = tea.String(fmt.Sprintf(OOSEndpointFormat, region))
	oosClient, err := oos20190601.NewClient(config)
	if err != nil {
		return nil, err
	}

	config.Endpoint = tea.String(fmt.Sprintf(CSEndpointFormat, region))
	csClient, err := cs20151215.NewClient(config)
	if err != nil {
		return nil, err
	}

	commonKMSKeyID := os.Getenv("KMS_KEY_ID")
	if commonKMSKeyID == "" {
		return nil, fmt.Errorf("required environment variable KMS_KEY_ID is not set; it is required to create the test KMS credentials")
	}
	commonKMSInstanceID := os.Getenv("KMS_INSTANCE_ID")
	if commonKMSInstanceID == "" {
		return nil, fmt.Errorf("required environment variable KMS_INSTANCE_ID is not set; it is required to build the dedicated gateway endpoint and create the test KMS credentials")
	}
	return &ResourceManager{
		kmsClient:           kmsClient,
		ramClient:           ramClient,
		oosClient:           oosClient,
		csClient:            csClient,
		accountID:           accountID,
		clusterID:           clusterID,
		regionID:            region,
		commonKMSInstanceID: commonKMSInstanceID,
		commonKMSKeyID:      commonKMSKeyID,
	}, nil
}

// SetRemoteAccountCredentials configures the ResourceManager with target account (Account B) credentials.
// When configured, cross-account tests will create resources in the target account using these credentials,
// ensuring true cross-account testing.
// When not configured, cross-account resource creation is skipped gracefully and
// cross-account tests are Skipped (no same-account simulation).
// remoteKMSKeyID and remoteKMSInstanceID are required for creating KMS secrets in the target account.
func (rm *ResourceManager) SetRemoteAccountCredentials(remoteAccountID, accessKeyID, accessKeySecret, remoteKMSKeyID, remoteKMSInstanceID string) error {
	if remoteAccountID == "" || accessKeyID == "" || accessKeySecret == "" {
		return nil // Cross-account not configured, cross-account resources will be skipped
	}

	rm.remoteAccountID = remoteAccountID
	rm.remoteKMSKeyID = remoteKMSKeyID
	rm.remoteKMSInstanceID = remoteKMSInstanceID

	// Create credential for remote account
	remoteCredConfig := &credential.Config{
		Type:            tea.String("access_key"),
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
	}
	remoteCred, err := credential.NewCredential(remoteCredConfig)
	if err != nil {
		return fmt.Errorf("failed to create remote account credential: %v", err)
	}

	// Create remote RAM client (uses target account's credentials)
	ramConfig := &openapi.Config{
		Credential: remoteCred,
		Endpoint:   tea.String(RAMEndpointFormat),
	}
	rm.remoteRamClient, err = ram20150501.NewClient(ramConfig)
	if err != nil {
		return fmt.Errorf("failed to create remote RAM client: %v", err)
	}

	// Create remote KMS client (uses target account's credentials)
	kmsConfig := &openapi.Config{
		Credential: remoteCred,
		Endpoint:   tea.String(fmt.Sprintf(SharedKMSEndpointFormat, rm.regionID)),
	}
	rm.remoteKmsClient, err = kms20160120.NewClient(kmsConfig)
	if err != nil {
		return fmt.Errorf("failed to create remote KMS client: %v", err)
	}

	log.Printf("Cross-account testing enabled: remote account ID = %s, KMS key = %s, KMS instance = %s", remoteAccountID, remoteKMSKeyID, remoteKMSInstanceID)
	return nil
}

// SetupTestResources creates all required test resources once
func (rm *ResourceManager) SetupTestResources(ctx context.Context) error {
	var err error
	// Set endpoint configuration for testing (consumed by specs via globals)
	DedicatedKMSEndpoint = fmt.Sprintf(DedicatedKMSEndpointFormat, rm.commonKMSInstanceID)
	SharedKMSEndpoint = fmt.Sprintf(SharedKMSEndpointFormat, rm.regionID)

	err = rm.CreateNamespace()
	if err != nil {
		return fmt.Errorf("failed to create namespace: %v", err)
	}

	// Get cluster details for some information
	ClusterWorkerRoleName, ClusterVPCID, ClusterVSwitchID, err = rm.GetClusterInfos(rm.clusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster worker role name: %v", err)
	}

	// Bind VPC for dedicated gateway testing
	err = rm.BindVPCForDedicatedGateway(ctx, rm.commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to bind VPC for dedicated gateway: %v", err)
	}

	// Create common KMS credential
	CommonKMSSecretName, err = rm.CreateCommonKMSCredential(ctx, rm.commonKMSKeyID, rm.commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create common KMS credential: %v", err)
	}

	// Create structured KMS credentials (JSON and YAML)
	JsonKMSSecretName, YamlKMSSecretName, err = rm.CreateStructuredKMSCredentials(ctx, rm.commonKMSKeyID, rm.commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create structured KMS credentials: %v", err)
	}

	// Create template test KMS credentials
	err = rm.CreateTemplateTestCredentials(ctx, rm.commonKMSKeyID, rm.commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create template test credentials: %v", err)
	}

	// Create advanced template test credentials for documentation examples
	err = rm.CreateAdvancedTemplateTestCredentials(ctx, rm.commonKMSKeyID, rm.commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create advanced template test credentials: %v", err)
	}

	// Create common OOS encrypted parameter
	CommonOOSSecretParameterName, err = rm.CreateCommonOOSSecretParameter(ctx)
	if err != nil {
		return fmt.Errorf("failed to create common OOS encrypted parameter: %v", err)
	}

	// Create policy for test resources
	PolicyNameForAccess, err = rm.CreatePolicy(`{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kms:GetSecretValue",
        "kms:Decrypt",
        "oos:GetSecretParameter",
        "sts:AssumeRole"
      ],
      "Resource": "*"
    }
  ]
}`, "access-policy-")
	if err != nil {
		return fmt.Errorf("failed to create policy: %v", err)
	}

	// Grant permissions to WorkerRole
	err = rm.AttachPolicyToRole(ClusterWorkerRoleName, PolicyNameForAccess)
	if err != nil {
		return fmt.Errorf("failed to grant WorkerRole permissions: %v", err)
	}

	// Create RAM user for Access Key authentication
	RAMUserAccessKeyID, RAMUserAccessKeySecret, RAMUserNameForAccessKey, err = rm.CreateRamUserForAccessKey(ctx)
	if err != nil {
		return fmt.Errorf("failed to create RAM user for access key: %v", err)
	}

	// Create RAM role for RRSA
	RAMRoleArnForRRSA, RAMRoleNameForRRSA, err = rm.CreateRamRoleForRRSA(ctx)
	if err != nil {
		return fmt.Errorf("failed to create RAM role for RRSA: %v", err)
	}
	OIDCProviderARN = fmt.Sprintf("acs:ram::%s:oidc-provider/ack-rrsa-%s", rm.accountID, rm.clusterID)

	// Create RAM role and user for role play
	RAMRoleArnForRolePlay, RAMUserAccessKeyIDForRolePlay, RAMUserAccessKeySecretForRolePlay, RAMRoleNameForRolePlay, RAMUserNameForRolePlay, err = rm.CreateRamRoleUserForRolePlay(ctx)
	if err != nil {
		return fmt.Errorf("failed to create RAM role and user for role play: %v", err)
	}

	// Create RAM role for service account authentication
	RAMRoleArnForSAAuth, RAMRoleNameForSAAuth, err = rm.CreateRamRoleServiceAccountForAuth(ctx)
	if err != nil {
		return fmt.Errorf("failed to create RAM role for SA auth: %v", err)
	}

	// Create RAM role for cross-account authentication (skipped gracefully when
	// remote account credentials are not configured)
	RAMRoleArnForCrossAccount, RAMRoleNameForCrossAccount, err = rm.CreateRamRoleForCrossAccount(ctx)
	if err != nil {
		return fmt.Errorf("failed to create RAM role for cross-account: %v", err)
	}

	// Create KMS secret in the target account for cross-account testing (skipped
	// gracefully when remote account credentials are not configured)
	CrossAccountKMSSecretName, err = rm.CreateRemoteKMSCredential(ctx)
	if err != nil {
		return fmt.Errorf("failed to create remote KMS secret for cross-account: %v", err)
	}

	err = rm.CreateServiceAccount()
	if err != nil {
		return fmt.Errorf("failed to create service account: %v", err)
	}

	log.Printf("Successfully set up all test resources!")

	return nil
}

// CreateNamespace creates a namespace for service account authentication testing
func (rm *ResourceManager) CreateNamespace() error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-serviceaccount-auth-" + getRandString(),
		},
	}
	err := k8sClient.Create(context.Background(), namespace)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}

	ServiceaccountNamespaceForSAAuth = namespace
	return nil
}

// GetClusterInfos gets the WorkerRole name, VPC ID and vSwitch ID of the
// cluster based on cluster ID
func (rm *ResourceManager) GetClusterInfos(clusterID string) (string, string, string, error) {
	resp, err := rm.csClient.DescribeClusterDetail(tea.String(clusterID))
	if err != nil {
		return "", "", "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.VswitchIds == nil || len(resp.Body.VswitchIds) < 1 {
		return "", "", "", fmt.Errorf("invalid response from DescribeClusterDetail")
	}

	return tea.StringValue(resp.Body.WorkerRamRoleName), tea.StringValue(resp.Body.VpcId), tea.StringValue(resp.Body.VswitchIds[0]), nil
}

// BindVPCForDedicatedGateway binds VPC to KMS instance for dedicated gateway testing
func (rm *ResourceManager) BindVPCForDedicatedGateway(ctx context.Context, kmsInstanceId string) error {
	accountID, err := strconv.Atoi(rm.accountID)
	if err != nil {
		return err
	}

	bindVPCReq := &kms20160120.UpdateKmsInstanceBindVpcRequest{}
	bindVPCReq.KmsInstanceId = tea.String(kmsInstanceId)
	bindVPCReq.BindVpcs = tea.String(fmt.Sprintf(`[{"VpcId":"%s","VSwitchId":"%s","RegionID":"%s", "VpcOwnerId": %d}]`, ClusterVPCID, ClusterVSwitchID, rm.regionID, accountID))

	_, err = rm.kmsClient.UpdateKmsInstanceBindVpc(bindVPCReq)

	return err
}

// CreateCommonKMSCredential create a common KMS credential
func (rm *ResourceManager) CreateCommonKMSCredential(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, error) {
	// Create a secret in KMS
	secretName := TestResourcePrefix + "common-kms-secret-" + getRandString()
	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(CommonKMSSecretValue)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", err
	}

	return secretName, nil
}

// CreateKMSSecretForCredentialUpdate creates a KMS secret dedicated to a single test
// case, so that credential-update tests do not mutate the shared CommonKMSSecretName
// (which other specs may read without a VersionId). The initial version is v1 with
// plain-text data; tests may append a new version via PutSecretValueForKMSSecret.
func (rm *ResourceManager) CreateKMSSecretForCredentialUpdate(ctx context.Context) (string, error) {
	secretName := TestResourcePrefix + "reconcile-update-secret-" + getRandString()

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(CredentialUpdateKMSSecretInitialValue)
	createSecretReq.DKMSInstanceId = tea.String(rm.commonKMSInstanceID)
	createSecretReq.EncryptionKeyId = tea.String(rm.commonKMSKeyID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", fmt.Errorf("failed to create dedicated KMS secret for credential update test: %v", err)
	}

	return secretName, nil
}

// CreateKMSSecretWithData creates a KMS secret dedicated to a single test
// case whose v1 version carries the given (typically JSON) payload, so specs
// that need specific initial document content (e.g. the zero-output guard
// spec) never mutate the shared CommonKMSSecretName. Tests may append further
// versions via PutKMSSecretVersion; cleanup is owned by the caller via
// DeleteKMSSecret (mirrors CreateKMSSecretForCredentialUpdate).
func (rm *ResourceManager) CreateKMSSecretWithData(ctx context.Context, secretData string) (string, error) {
	secretName := TestResourcePrefix + "data-driven-secret-" + getRandString()

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(secretData)
	createSecretReq.DKMSInstanceId = tea.String(rm.commonKMSInstanceID)
	createSecretReq.EncryptionKeyId = tea.String(rm.commonKMSKeyID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", fmt.Errorf("failed to create dedicated KMS secret with custom data: %v", err)
	}

	return secretName, nil
}

// PutSecretValueForKMSSecret appends a new version to the given KMS secret.
func (rm *ResourceManager) PutSecretValueForKMSSecret(ctx context.Context, secretName string) error {
	putSecretValueReq := &kms20160120.PutSecretValueRequest{}
	putSecretValueReq.SecretName = tea.String(secretName)
	putSecretValueReq.VersionId = tea.String("v2")
	putSecretValueReq.SecretData = tea.String(`{"key1":"value1","key2":"value2"}`)

	_, err := rm.kmsClient.PutSecretValue(putSecretValueReq)
	return err
}

// PutKMSSecretVersion appends a new version with the given version id and
// value to the given KMS secret. Unlike PutSecretValueForKMSSecret (fixed
// v2), it lets specs stage multiple successive remote values so watch-driven
// re-syncs always have a fresh value to observe.
func (rm *ResourceManager) PutKMSSecretVersion(ctx context.Context, secretName, versionID, value string) error {
	putSecretValueReq := &kms20160120.PutSecretValueRequest{}
	putSecretValueReq.SecretName = tea.String(secretName)
	putSecretValueReq.VersionId = tea.String(versionID)
	putSecretValueReq.SecretData = tea.String(value)

	_, err := rm.kmsClient.PutSecretValue(putSecretValueReq)
	return err
}

// DeleteKMSSecret force-deletes the given KMS secret. Tolerates already-deleted
// secrets so it is safe to call from DeferCleanup.
func (rm *ResourceManager) DeleteKMSSecret(secretName string) error {
	if secretName == "" {
		return nil
	}
	deleteSecretReq := &kms20160120.DeleteSecretRequest{}
	deleteSecretReq.SecretName = tea.String(secretName)
	deleteSecretReq.ForceDeleteWithoutRecovery = tea.String("true")
	_, err := rm.kmsClient.DeleteSecret(deleteSecretReq)
	if err != nil && !strings.Contains(err.Error(), "Resource not found") {
		return fmt.Errorf("failed to delete KMS secret %s: %v", secretName, err)
	}
	return nil
}

// CreateStructuredKMSCredentials creates KMS credentials with JSON/YAML data
func (rm *ResourceManager) CreateStructuredKMSCredentials(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, string, error) {
	// Create secret with JSON data
	jsonSecretName := TestResourcePrefix + "structured-json-secret-" + getRandString()
	jsonData := `{"name":"xiaoming","age":10,"friends":[{"name":"xiaohong","age":11},{"name":"xiaoli","age":12}]}`

	createJsonSecretReq := &kms20160120.CreateSecretRequest{}
	createJsonSecretReq.SecretName = tea.String(jsonSecretName)
	createJsonSecretReq.VersionId = tea.String("v1")
	createJsonSecretReq.SecretData = tea.String(jsonData)
	createJsonSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createJsonSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err := rm.kmsClient.CreateSecret(createJsonSecretReq)
	if err != nil {
		return "", "", err
	}

	// Create secret with YAML data
	yamlSecretName := TestResourcePrefix + "structured-yaml-secret-" + getRandString()
	yamlData := `name: xiaoming
age: 10
friends:
  - name: xiaohong
    age: 11
  - name: xiaoli
    age: 12
`

	createYamlSecretReq := &kms20160120.CreateSecretRequest{}
	createYamlSecretReq.SecretName = tea.String(yamlSecretName)
	createYamlSecretReq.VersionId = tea.String("v1")
	createYamlSecretReq.SecretData = tea.String(yamlData)
	createYamlSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createYamlSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err = rm.kmsClient.CreateSecret(createYamlSecretReq)
	if err != nil {
		return jsonSecretName, "", err
	}

	return jsonSecretName, yamlSecretName, nil
}

// CreateTemplateTestCredentials creates KMS credentials for template testing
// Optimized to create only essential secrets needed for all tests
func (rm *ResourceManager) CreateTemplateTestCredentials(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) error {
	var err error

	// Create simple template test secret with multiple data formats for various tests
	// Contains: key=value pairs, nested JSON, arrays, and configuration data
	SimpleTemplateSecretName, err = rm.CreateSimpleTemplateSecret(ctx, commonKMSKeyID, commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create simple template secret: %v", err)
	}

	// Create Go template test secret with structured JSON data
	// Used for: JSON path queries, type conversions, array/slice operations, default values
	GoTemplateSecretName, err = rm.CreateGoTemplateSecret(ctx, commonKMSKeyID, commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create go template secret: %v", err)
	}

	// Create auth credentials secret for htpasswd testing
	AuthCredentialsSecretName, err = rm.CreateAuthCredentialsSecret(ctx, commonKMSKeyID, commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create auth credentials secret: %v", err)
	}

	// Create merge JSON test data (two separate secrets for base and override)
	MergeJsonBaseSecretName, MergeJsonOverrideSecretName, err = rm.CreateMergeJsonTestData(ctx, commonKMSKeyID, commonKMSInstanceID)
	if err != nil {
		return fmt.Errorf("failed to create merge JSON test data: %v", err)
	}

	return nil
}

// CreateSimpleTemplateSecret creates a secret with simple key-value pairs for basic template testing
func (rm *ResourceManager) CreateSimpleTemplateSecret(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, error) {
	secretName := TestResourcePrefix + "simple-template-secret-" + getRandString()
	simpleData := `key1=value1
key2=value2
status=enabled
name=test-app`

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(simpleData)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", err
	}

	return secretName, nil
}

// CreateGoTemplateSecret creates a secret with structured data for Go template testing
func (rm *ResourceManager) CreateGoTemplateSecret(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, error) {
	secretName := TestResourcePrefix + "go-template-secret-" + getRandString()
	goData := `{
    "appName": "myapp",
    "environment": "production",
    "status": "enabled",
    "database": {
        "host": "db.example.com",
        "port": 5432,
        "name": "mydatabase"
    },
    "features": ["auth", "logging", "monitoring"],
    "replicas": 3,
    "enabled": true,
    "users": [
        {"name": "alice", "age": 30, "active": true, "role": "admin"},
        {"name": "bob", "age": 25, "active": false, "role": "user"},
        {"name": "charlie", "age": 35, "active": true, "role": "user"}
    ],
    "tags": ["web", "api", "v2"],
    "ports": [8080, 8081, 8082],
    "string_number": "123",
    "string_boolean_true": "true",
    "string_boolean_yes": "yes",
    "string_boolean_false": "false",
    "string_boolean_no": "no",
    "array_of_numbers": [1, 2, 3, 4, 5],
    "mixed_array": ["one", {"key": "two"}, "three"],
    "json_object": {
        "value": "two",
        "nested": {
            "inner": "inner-value"
        }
    },
    "config": "{\"app\": \"myapp\", \"debug\": false}",
    "version": "v1.2.3"
}`

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(goData)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", err
	}

	return secretName, nil
}

// CreateAuthCredentialsSecret creates a secret with username and password for authentication testing
func (rm *ResourceManager) CreateAuthCredentialsSecret(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, error) {
	secretName := TestResourcePrefix + "auth-credentials-secret-" + getRandString()
	// Use JSON format so template engine can extract individual fields
	authData := `{
    "username": "admin",
    "password": "SecurePass123"
}`

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(authData)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", err
	}

	return secretName, nil
}

// CreateMergeJsonTestData creates two separate JSON secrets for testing mergeJson function
func (rm *ResourceManager) CreateMergeJsonTestData(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) (string, string, error) {
	// Base JSON with nested structure
	baseSecretName := TestResourcePrefix + "mergejson-base-" + getRandString()
	baseData := `{
    "app": {
        "name": "myapp",
        "version": "1.0.0"
    },
    "database": {
        "host": "localhost",
        "port": 5432,
        "name": "devdb"
    },
    "logging": {
        "level": "debug",
        "format": "json"
    }
}`

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(baseSecretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(baseData)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", "", err
	}

	// Override JSON with updates and new fields
	overrideSecretName := TestResourcePrefix + "mergejson-override-" + getRandString()
	overrideData := `{
    "app": {
        "version": "2.0.0",
        "environment": "production"
    },
    "database": {
        "host": "prod-db.example.com",
        "password": "secret123"
    },
    "cache": {
        "enabled": true,
        "ttl": 3600
    }
}`

	createSecretReq.SecretName = tea.String(overrideSecretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(overrideData)
	createSecretReq.EncryptionKeyId = tea.String(commonKMSKeyID)
	createSecretReq.DKMSInstanceId = tea.String(commonKMSInstanceID)

	_, err = rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", "", err
	}

	return baseSecretName, overrideSecretName, nil
}

// CreateAdvancedTemplateTestCredentials creates KMS secrets for advanced template examples from documentation
func (rm *ResourceManager) CreateAdvancedTemplateTestCredentials(ctx context.Context, commonKMSKeyID, commonKMSInstanceID string) error {
	var err error

	// 5.2.1 Microservice configuration generation example required data
	AdvancedServiceMetadataSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "service-metadata", `{"name": "user-service", "version": "v1.2.3"}`)
	if err != nil {
		return fmt.Errorf("failed to create service metadata secret: %w", err)
	}

	AdvancedEnvironmentConfigSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "environment-config", "dev")
	if err != nil {
		return fmt.Errorf("failed to create environment config secret: %w", err)
	}

	AdvancedDatabaseCredsSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "database-creds", `{"user": "dbuser", "password": "dbpass123", "host": "postgres.internal", "port": "5432", "name": "users_db"}`)
	if err != nil {
		return fmt.Errorf("failed to create database creds secret: %w", err)
	}

	AdvancedRedisConfigSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "redis-config", `{"host": "redis.cache.internal", "port": "6379", "db": "1"}`)
	if err != nil {
		return fmt.Errorf("failed to create redis config secret: %w", err)
	}

	AdvancedLoggingConfigSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "logging-config", `{"level": "debug", "format": "text"}`)
	if err != nil {
		return fmt.Errorf("failed to create logging config secret: %w", err)
	}

	// 5.2.2 Kubernetes resource manifest generation example required data
	AdvancedAppNameSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "app-name", "my-web-app")
	if err != nil {
		return fmt.Errorf("failed to create app name secret: %w", err)
	}

	AdvancedAppVersionSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "app-version", "v2.1.0")
	if err != nil {
		return fmt.Errorf("failed to create app version secret: %w", err)
	}

	AdvancedReplicasSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "replicas", "5")
	if err != nil {
		return fmt.Errorf("failed to create replicas secret: %w", err)
	}

	AdvancedImageRepoSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "image-repository", "registry.aliyuncs.com/mycompany")
	if err != nil {
		return fmt.Errorf("failed to create image repo secret: %w", err)
	}

	AdvancedImageTagSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "image-tag", "latest")
	if err != nil {
		return fmt.Errorf("failed to create image tag secret: %w", err)
	}

	AdvancedPortsSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "ports", `[8080, 8443, 9090]`)
	if err != nil {
		return fmt.Errorf("failed to create ports secret: %w", err)
	}

	// 5.2.3 Certificate and key handling example required data
	privateKey := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC2vV8Q9F3W4X5m
2K9j8N7p1q3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q0CAwEA
AQ==
-----END PRIVATE KEY-----`

	AdvancedPrivateKeySecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "private-key", privateKey)
	if err != nil {
		return fmt.Errorf("failed to create private key secret: %w", err)
	}

	certificate := `-----BEGIN CERTIFICATE-----
MIIDXTCCAkWgAwIBAgIJAK/S5jYubNBFMA0GCSqGSIb3DQEBCwUAMEUxCzAJBgNV
BAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBX
aWRnaXRzIFB0eSBMdGQwHhcNMjMwMjI3MDAwMDAwWhcNMzMwMjI3MDAwMDAwWjBF
MQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwYSW50
ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIB
CgKCAQEA2vV8Q9F3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q0
-----END CERTIFICATE-----`

	AdvancedCertificateSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "certificate", certificate)
	if err != nil {
		return fmt.Errorf("failed to create certificate secret: %w", err)
	}

	caBundle := `-----BEGIN CERTIFICATE-----
MIIDXTCCAkWgAwIBAgIJAK/S5jYubNBFMA0GCSqGSIb3DQEBCwUAMEUxCzAJBgNV
BAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBX
aWRnaXRzIFB0eSBMdGQwHhcNMjMwMjI3MDAwMDAwWhcNMzMwMjI3MDAwMDAwWjBF
MQswCQYDVQQGEwJBVTETMBEGA1UECAwKU29tZS1TdGF0ZTEhMB8GA1UECgwYSW50
ZXJuZXQgV2lkZ2l0cyBQdHkgTHRkMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIB
CgKCAQEA2vV8Q9F3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q3W4X5m2K9j8N7p1q0
-----END CERTIFICATE-----`

	AdvancedCABundleSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "ca-bundle", caBundle)
	if err != nil {
		return fmt.Errorf("failed to create CA bundle secret: %w", err)
	}

	AdvancedKeystorePasswordSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "keystore-password", "mySecurePass123")
	if err != nil {
		return fmt.Errorf("failed to create keystore password secret: %w", err)
	}

	// 5.3.1 Multi-environment configuration management example required data
	AdvancedCurrentEnvironmentSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "current-environment", "staging")
	if err != nil {
		return fmt.Errorf("failed to create current environment secret: %w", err)
	}

	AdvancedServiceNameSecret, err = rm.createAdvancedSecret(commonKMSKeyID, commonKMSInstanceID, "service-name", "payment-service")
	if err != nil {
		return fmt.Errorf("failed to create service name secret: %w", err)
	}

	return nil
}

// Helper function to create advanced template test secrets
func (rm *ResourceManager) createAdvancedSecret(keyID, instanceID, name, data string) (string, error) {
	secretName := TestResourcePrefix + "advanced-" + name + "-" + getRandString()

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String(data)
	createSecretReq.EncryptionKeyId = tea.String(keyID)
	createSecretReq.DKMSInstanceId = tea.String(instanceID)

	_, err := rm.kmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", fmt.Errorf("failed to create advanced secret %s: %w", name, err)
	}

	return secretName, nil
}

// CreateCommonOOSSecretParameter create a OOS secret parameter
func (rm *ResourceManager) CreateCommonOOSSecretParameter(ctx context.Context) (string, error) {
	// Create OOS encrypted parameter
	oosParamName := TestResourcePrefix + "oos-encrypted-param-" + getRandString()
	createOOSSecretParamReq := &oos20190601.CreateSecretParameterRequest{}
	createOOSSecretParamReq.RegionId = tea.String(rm.regionID)
	createOOSSecretParamReq.Name = tea.String(oosParamName)
	createOOSSecretParamReq.Value = tea.String(CommonOOSSecretParameterValue)

	_, err := rm.oosClient.CreateSecretParameter(createOOSSecretParamReq)
	if err != nil {
		return "", err
	}

	return oosParamName, nil
}

// CreateOOSSecretParameterForUpdate creates a DEDICATED OOS secret parameter
// seeded with initialValue, intended for specs that stage new remote values
// via PutOOSParameterValue. It deliberately does not touch the shared
// CommonOOSSecretParameterName, whose value other specs (provider_test.go)
// assert by equality.
func (rm *ResourceManager) CreateOOSSecretParameterForUpdate(ctx context.Context, initialValue string) (string, error) {
	oosParamName := oosWatchParamNamePrefix + getRandString()
	createOOSSecretParamReq := &oos20190601.CreateSecretParameterRequest{}
	createOOSSecretParamReq.RegionId = tea.String(rm.regionID)
	createOOSSecretParamReq.Name = tea.String(oosParamName)
	createOOSSecretParamReq.Value = tea.String(initialValue)

	_, err := rm.oosClient.CreateSecretParameter(createOOSSecretParamReq)
	if err != nil {
		return "", err
	}

	return oosParamName, nil
}

// PutOOSParameterValue updates an OOS secret parameter to a new value; the
// update produces a new latest parameter version that only a fresh sync round
// can observe (OOS counterpart of PutKMSSecretVersion).
func (rm *ResourceManager) PutOOSParameterValue(ctx context.Context, name, value string) error {
	updateReq := &oos20190601.UpdateSecretParameterRequest{}
	updateReq.RegionId = tea.String(rm.regionID)
	updateReq.Name = tea.String(name)
	updateReq.Value = tea.String(value)

	_, err := rm.oosClient.UpdateSecretParameter(updateReq)
	return err
}

// DeleteOOSSecretParameter deletes an OOS secret parameter by name
// (OOS counterpart of DeleteKMSSecret; used by per-spec DeferCleanup).
func (rm *ResourceManager) DeleteOOSSecretParameter(name string) error {
	deleteReq := &oos20190601.DeleteSecretParameterRequest{}
	deleteReq.Name = tea.String(name)

	_, err := rm.oosClient.DeleteSecretParameter(deleteReq)
	return err
}

// sweepOOSSecretParametersByPrefix is the best-effort fallback sweep of the
// AfterSuite cleanup: it lists OOS secret parameters page by page (the Name
// filter is a fuzzy keyword match, so every candidate is re-checked with an
// exact prefix test) and deletes the leftovers whose name starts with the
// given prefix (the dedicated watch parameters created via
// CreateOOSSecretParameterForUpdate whose per-spec DeferCleanup failed or
// never ran). Every failure is logged as a WARNING only -- the sweep must
// never block or mask the suite result.
func (rm *ResourceManager) sweepOOSSecretParametersByPrefix(prefix string) {
	var nextToken *string
	// Page cap guards against a pathological NextToken loop; 20 pages of up
	// to 100 entries far exceeds any realistic leftover count.
	for page := 0; page < 20; page++ {
		listReq := &oos20190601.ListSecretParametersRequest{}
		listReq.RegionId = tea.String(rm.regionID)
		listReq.Name = tea.String(prefix)
		listReq.NextToken = nextToken
		resp, err := rm.oosClient.ListSecretParameters(listReq)
		if err != nil || resp == nil || resp.Body == nil {
			log.Printf("WARNING: fallback sweep could not list OOS secret parameters (prefix %s): %v", prefix, err)
			return
		}
		for _, param := range resp.Body.Parameters {
			if param == nil || param.Name == nil || !strings.HasPrefix(*param.Name, prefix) {
				continue
			}
			if err := rm.DeleteOOSSecretParameter(*param.Name); err != nil {
				log.Printf("WARNING: fallback sweep failed to delete leftover OOS secret parameter %s: %v", *param.Name, err)
			} else {
				log.Printf("fallback sweep deleted leftover OOS secret parameter %s", *param.Name)
			}
		}
		if resp.Body.NextToken == nil || *resp.Body.NextToken == "" {
			return
		}
		nextToken = resp.Body.NextToken
	}
	log.Printf("WARNING: fallback sweep stopped after 20 pages; leftover OOS secret parameters with prefix %s may remain", prefix)
}

func (rm *ResourceManager) CreatePolicy(policyDocument, policyNameInfix string) (string, error) {
	policyName := TestResourcePrefix + policyNameInfix + getRandString()
	createPolicyReq := &ram20150501.CreatePolicyRequest{}
	createPolicyReq.PolicyName = tea.String(policyName)
	createPolicyReq.PolicyDocument = tea.String(policyDocument)

	_, err := rm.ramClient.CreatePolicy(createPolicyReq)
	if err != nil {
		return "", err
	}

	return policyName, nil
}

// AttachPolicyToRole grants permissions to the Ram Role for authentication testing
func (rm *ResourceManager) AttachPolicyToRole(roleName, policyName string) error {
	attachPolicyReq := &ram20150501.AttachPolicyToRoleRequest{}
	attachPolicyReq.PolicyType = tea.String(PolicyType)
	attachPolicyReq.PolicyName = tea.String(policyName)
	attachPolicyReq.RoleName = tea.String(roleName)
	_, err := rm.ramClient.AttachPolicyToRole(attachPolicyReq)
	if err != nil {
		return err
	}

	return nil
}

// AttachPolicyToUser grants permissions to the Ram User for authentication testing
func (rm *ResourceManager) AttachPolicyToUser(userName, policyName string) error {
	attachPolicyReq := &ram20150501.AttachPolicyToUserRequest{}
	attachPolicyReq.PolicyType = tea.String(PolicyType)
	attachPolicyReq.PolicyName = tea.String(policyName)
	attachPolicyReq.UserName = tea.String(userName)
	_, err := rm.ramClient.AttachPolicyToUser(attachPolicyReq)
	if err != nil {
		return err
	}

	return nil
}

func (rm *ResourceManager) CreateRamUserForAccessKey(ctx context.Context) (string, string, string, error) {
	userName := TestResourcePrefix + "access-key-user-" + getRandString()
	createUserReq := &ram20150501.CreateUserRequest{}
	createUserReq.UserName = tea.String(userName)

	_, err := rm.ramClient.CreateUser(createUserReq)
	if err != nil {
		return "", "", "", err
	}

	err = rm.AttachPolicyToUser(userName, PolicyNameForAccess)
	if err != nil {
		return "", "", userName, err
	}

	// Create access key for the user
	createAccessKeyReq := &ram20150501.CreateAccessKeyRequest{}
	createAccessKeyReq.UserName = tea.String(userName)

	resp, err := rm.ramClient.CreateAccessKey(createAccessKeyReq)
	if err != nil {
		return "", "", userName, err
	}

	accessKeyId := tea.StringValue(resp.Body.AccessKey.AccessKeyId)
	accessKeySecret := tea.StringValue(resp.Body.AccessKey.AccessKeySecret)

	return accessKeyId, accessKeySecret, userName, nil
}

// CreateRamUserWithoutKMSPermission creates a RAM user with NO policy attached
// and returns its access key. The AK is used as a negative discriminator in the
// authentication priority test: any KMS call authenticated by this AK fails
// with a permanent 403 permission error.
func (rm *ResourceManager) CreateRamUserWithoutKMSPermission(ctx context.Context) (string, string, string, error) {
	userName := TestResourcePrefix + "no-kms-permission-user-" + getRandString()
	createUserReq := &ram20150501.CreateUserRequest{}
	createUserReq.UserName = tea.String(userName)

	_, err := rm.ramClient.CreateUser(createUserReq)
	if err != nil {
		return "", "", "", err
	}

	// Intentionally do NOT attach any policy: the user must have no KMS permission.

	// Create access key for the user
	createAccessKeyReq := &ram20150501.CreateAccessKeyRequest{}
	createAccessKeyReq.UserName = tea.String(userName)

	resp, err := rm.ramClient.CreateAccessKey(createAccessKeyReq)
	if err != nil {
		// best-effort rollback to avoid leaking the RAM user
		deleteUserReq := &ram20150501.DeleteUserRequest{}
		deleteUserReq.UserName = tea.String(userName)
		_, _ = rm.ramClient.DeleteUser(deleteUserReq)
		return "", "", userName, err
	}

	accessKeyId := tea.StringValue(resp.Body.AccessKey.AccessKeyId)
	accessKeySecret := tea.StringValue(resp.Body.AccessKey.AccessKeySecret)

	return accessKeyId, accessKeySecret, userName, nil
}

// DeleteRamUserAndAccessKey detaches all policies attached to the user, deletes
// the given access key (if non-empty) and then deletes the user. It tolerates
// not-found errors ("Resource not found" and RAM OpenAPI's "EntityNotExist"
// family, e.g. EntityNotExist.User) so it is idempotent and safe to call from
// DeferCleanup.
func (rm *ResourceManager) DeleteRamUserAndAccessKey(userName, accessKeyID string) error {
	err := rm.deleteRamUser(userName, accessKeyID)
	if err != nil && (strings.Contains(err.Error(), "Resource not found") || strings.Contains(err.Error(), "EntityNotExist")) {
		return nil
	}
	return err
}

// DeleteRamRole detaches all policies attached to the role and then deletes
// the role in the source account. It tolerates not-found errors ("Resource
// not found" and RAM OpenAPI's "EntityNotExist" family, e.g.
// EntityNotExist.Role) so it is idempotent and safe to call from DeferCleanup.
func (rm *ResourceManager) DeleteRamRole(roleName string) error {
	err := rm.deleteRamRole(roleName)
	if err != nil && (strings.Contains(err.Error(), "Resource not found") || strings.Contains(err.Error(), "EntityNotExist")) {
		return nil
	}
	return err
}

// CreateRamRoleForRRSA creates a RAM role for RRSA testing
func (rm *ResourceManager) CreateRamRoleForRRSA(ctx context.Context) (string, string, error) {
	roleName := TestResourcePrefix + "rrsa-role-" + getRandString()

	// Trust relationship for RRSA (Service Account)
	policyDocument := fmt.Sprintf(`{
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "oidc:aud": [
            "sts.aliyuncs.com"
          ],
          "oidc:iss": [
            "https://oidc-ack-%s.oss-%s.aliyuncs.com/%s"
          ],
          "oidc:sub": [
            "system:serviceaccount:kube-system:ack-secret-manager"
          ]
        }
      },
      "Effect": "Allow",
      "Principal": {
        "Federated": [
          "acs:ram::%s:oidc-provider/ack-rrsa-%s"
        ]
      }
    }
  ],
  "Version": "1"
}
`, rm.regionID, rm.regionID, rm.clusterID, rm.accountID, rm.clusterID)

	createRoleReq := &ram20150501.CreateRoleRequest{}
	createRoleReq.RoleName = tea.String(roleName)
	createRoleReq.AssumeRolePolicyDocument = tea.String(policyDocument)

	resp, err := rm.ramClient.CreateRole(createRoleReq)
	if err != nil {
		return "", "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.Role == nil {
		return "", roleName, fmt.Errorf("invalid response from CreateRole")
	}
	roleArn := tea.StringValue(resp.Body.Role.Arn)

	err = rm.AttachPolicyToRole(roleName, PolicyNameForAccess)
	if err != nil {
		return "", roleName, err
	}

	return roleArn, roleName, nil
}

// CreateRamRoleUserForRolePlay creates RAM role and user for role play testing
func (rm *ResourceManager) CreateRamRoleUserForRolePlay(ctx context.Context) (string, string, string, string, string, error) {
	roleName := TestResourcePrefix + "role-play-role-" + getRandString()
	userName := TestResourcePrefix + "role-play-user-" + getRandString()

	// Create RAM role with proper trust relationship.
	// Trust both the account root (for AK+AssumeRole) and the OIDC provider (for OIDC-based auth);
	// under this AK+roleArn combination AK AssumeRole takes effect (auto-derived OIDC does not
	// enter the auth chain when complete AK is configured), and the dual trust policy ensures
	// credentials obtained via the AK AssumeRole path can assume the role successfully.
	policyDocument := fmt.Sprintf(`{
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Effect": "Allow",
      "Principal": {
        "RAM": [
          "acs:ram::%s:root"
        ]
      }
    },
    {
      "Action": "sts:AssumeRole",
      "Effect": "Allow",
      "Principal": {
        "Federated": [
          "acs:ram::%s:oidc-provider/ack-rrsa-%s"
        ]
      }
    }
  ],
  "Version": "1"
}`, rm.accountID, rm.accountID, rm.clusterID)

	createRoleReq := &ram20150501.CreateRoleRequest{}
	createRoleReq.RoleName = tea.String(roleName)
	createRoleReq.AssumeRolePolicyDocument = tea.String(policyDocument)

	resp, err := rm.ramClient.CreateRole(createRoleReq)
	if err != nil {
		return "", "", "", "", "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.Role == nil {
		return "", "", "", roleName, "", fmt.Errorf("invalid response from CreateRole")
	}

	roleArn := tea.StringValue(resp.Body.Role.Arn)

	// Attach policy to the role
	err = rm.AttachPolicyToRole(roleName, PolicyNameForAccess)
	if err != nil {
		_ = rm.deleteRamRole(roleName)
		return "", "", "", roleName, "", err
	}

	// Create RAM user
	createUserReq := &ram20150501.CreateUserRequest{}
	createUserReq.UserName = tea.String(userName)

	_, err = rm.ramClient.CreateUser(createUserReq)
	if err != nil {
		return "", "", "", roleName, "", err
	}

	PolicyNameForRolePlay, err = rm.CreatePolicy(fmt.Sprintf(`
	{
    "Statement": [
        {
            "Action": "sts:AssumeRole",
            "Effect": "Allow",
            "Resource": "acs:ram:*:%s:role/%s"  
        }
    ],
    "Version": "1"
}`, rm.accountID, roleName), "role-play-policy-")

	if err != nil {
		return "", "", "", roleName, userName, err
	}

	// Attach policy to the user to allow AssumeRole
	err = rm.AttachPolicyToUser(userName, PolicyNameForRolePlay)
	if err != nil {
		return "", "", "", roleName, userName, err
	}

	// Create Access Key for the user
	createAccessKeyReq := &ram20150501.CreateAccessKeyRequest{}
	createAccessKeyReq.UserName = tea.String(userName)

	accessKeyResp, err := rm.ramClient.CreateAccessKey(createAccessKeyReq)
	if err != nil {
		return "", "", "", roleName, userName, err
	}
	if accessKeyResp == nil || accessKeyResp.Body == nil || accessKeyResp.Body.AccessKey == nil {
		return "", "", "", roleName, userName, fmt.Errorf("invalid response from CreateAccessKey")
	}

	accessKeyId := tea.StringValue(accessKeyResp.Body.AccessKey.AccessKeyId)
	accessKeySecret := tea.StringValue(accessKeyResp.Body.AccessKey.AccessKeySecret)

	return roleArn, accessKeyId, accessKeySecret, roleName, userName, nil
}

// CreateRamRoleForServiceAccount creates a RAM role whose trust policy allows
// the given ServiceAccount (saNamespace/saName) to assume it via RRSA, and
// attaches the KMS access policy. Returns the role ARN and role name.
func (rm *ResourceManager) CreateRamRoleForServiceAccount(ctx context.Context, saNamespace, saName string) (string, string, error) {
	roleName := TestResourcePrefix + "sa-auth-role-" + getRandString()

	// Create RAM role with trust relationship for service account
	policyDocument := fmt.Sprintf(`{
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "oidc:aud": [
            "sts.aliyuncs.com"
          ],
          "oidc:iss": [
            "https://oidc-ack-%s.oss-%s.aliyuncs.com/%s"
          ],
          "oidc:sub": [
            "system:serviceaccount:%s:%s"
          ]
        }
      },
      "Effect": "Allow",
      "Principal": {
        "Federated": [
          "acs:ram::%s:oidc-provider/ack-rrsa-%s"
        ]
      }
    }
  ],
  "Version": "1"
}`, rm.regionID, rm.regionID, rm.clusterID, saNamespace, saName, rm.accountID, rm.clusterID)

	createRoleReq := &ram20150501.CreateRoleRequest{}
	createRoleReq.RoleName = tea.String(roleName)
	createRoleReq.AssumeRolePolicyDocument = tea.String(policyDocument)

	resp, err := rm.ramClient.CreateRole(createRoleReq)
	if err != nil {
		return "", "", err
	}
	if resp == nil || resp.Body == nil || resp.Body.Role == nil {
		return "", roleName, fmt.Errorf("invalid response from CreateRole")
	}

	roleArn := tea.StringValue(resp.Body.Role.Arn)

	// Attach policy to the role
	err = rm.AttachPolicyToRole(roleName, PolicyNameForAccess)
	if err != nil {
		return "", roleName, err
	}

	return roleArn, roleName, nil
}

// CreateRamRoleServiceAccountForAuth creates RAM role and service account for service account authentication testing
func (rm *ResourceManager) CreateRamRoleServiceAccountForAuth(ctx context.Context) (string, string, error) {
	return rm.CreateRamRoleForServiceAccount(ctx, ServiceaccountNamespaceForSAAuth.Name, ServiceaccountNameForSAAuth)
}

// CreateRamRoleForCrossAccount creates a RAM role in the target account (Account B) for cross-account testing.
// The role's trust policy allows the source account (Account A) to assume it.
// A KMS access policy is also created in the target account and attached to the role.
func (rm *ResourceManager) CreateRamRoleForCrossAccount(ctx context.Context) (string, string, error) {
	// Cross-account credentials not configured: skip gracefully; the empty
	// result makes cross-account tests hit their Skip conditions and cleanup
	// return early.
	if rm.remoteRamClient == nil {
		log.Printf("Cross-account credentials not configured (CROSS_ACCOUNT_* env vars unset), skipping cross-account RAM role creation")
		return "", "", nil
	}

	roleName := TestResourcePrefix + "cross-account-role-" + getRandString()

	log.Printf("Creating cross-account role in target account %s, trusting source account %s", rm.remoteAccountID, rm.accountID)

	// Trust policy: allow the source account to assume this role.
	// The cross-account role is created in the TARGET account, so it can only trust
	// the source account's RAM root principal. OIDC provider exists in the source account,
	// not the target account, so cross-account OIDC Federated trust is not supported.
	// The cross-account flow is: OIDC auth (source) → AssumeRole (target with root trust).
	// Reference: advanced-02-cross-account.yaml
	policyDocument := fmt.Sprintf(`{
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Effect": "Allow",
      "Principal": {
        "RAM": [
          "acs:ram::%s:root"
        ]
      }
    }
  ],
  "Version": "1"
}`, rm.accountID)

	createRoleReq := &ram20150501.CreateRoleRequest{}
	createRoleReq.RoleName = tea.String(roleName)
	createRoleReq.AssumeRolePolicyDocument = tea.String(policyDocument)

	resp, err := rm.remoteRamClient.CreateRole(createRoleReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to create cross-account role: %v", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Role == nil {
		return "", roleName, fmt.Errorf("invalid response from CreateRole")
	}

	roleArn := tea.StringValue(resp.Body.Role.Arn)

	// Create and attach KMS access policy in the target account
	crossAccountPolicyName, err := rm.createCrossAccountKMSPolicy(rm.remoteRamClient, roleName)
	if err != nil {
		// Cleanup on failure
		deleteRoleReq := &ram20150501.DeleteRoleRequest{RoleName: tea.String(roleName)}
		_, _ = rm.remoteRamClient.DeleteRole(deleteRoleReq)
		return "", roleName, fmt.Errorf("failed to create cross-account KMS policy: %v", err)
	}

	// Store the policy name for cleanup
	CrossAccountPolicyName = crossAccountPolicyName
	log.Printf("Created cross-account KMS policy: %s in target account", crossAccountPolicyName)

	return roleArn, roleName, nil
}

// createCrossAccountKMSPolicy creates a KMS access policy and attaches it to the role.
// Uses the provided ramClient (which may be the remote account's client).
func (rm *ResourceManager) createCrossAccountKMSPolicy(ramClient *ram20150501.Client, roleName string) (string, error) {
	policyName := TestResourcePrefix + "cross-account-kms-policy-" + getRandString()

	policyDocument := `{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kms:GetSecretValue",
        "kms:Decrypt"
      ],
      "Resource": "*"
    }
  ]
}`

	createPolicyReq := &ram20150501.CreatePolicyRequest{}
	createPolicyReq.PolicyName = tea.String(policyName)
	createPolicyReq.PolicyDocument = tea.String(policyDocument)

	_, err := ramClient.CreatePolicy(createPolicyReq)
	if err != nil {
		return "", fmt.Errorf("failed to create policy: %v", err)
	}

	// Attach policy to the role
	attachReq := &ram20150501.AttachPolicyToRoleRequest{}
	attachReq.PolicyType = tea.String("Custom")
	attachReq.PolicyName = tea.String(policyName)
	attachReq.RoleName = tea.String(roleName)

	_, err = ramClient.AttachPolicyToRole(attachReq)
	if err != nil {
		return policyName, fmt.Errorf("failed to attach policy to role: %v", err)
	}

	return policyName, nil
}

// CreateRemoteKMSCredential creates a KMS secret in the target account (Account B) for cross-account testing.
// Requires remote account credentials to be configured via SetRemoteAccountCredentials.
func (rm *ResourceManager) CreateRemoteKMSCredential(ctx context.Context) (string, error) {
	// Cross-account credentials not configured: skip gracefully; the empty
	// result makes cross-account tests hit their Skip conditions and cleanup
	// return early.
	if rm.remoteKmsClient == nil {
		log.Printf("Cross-account credentials not configured (CROSS_ACCOUNT_* env vars unset), skipping cross-account KMS secret creation")
		return "", nil
	}

	// Cross-account is configured but the target-account KMS key/instance is
	// not provided: fail fast with a clear message listing the missing
	// variables, instead of calling CreateSecret with empty parameters.
	var missingVars []string
	if rm.remoteKMSKeyID == "" {
		missingVars = append(missingVars, "CROSS_ACCOUNT_KMS_KEY_ID")
	}
	if rm.remoteKMSInstanceID == "" {
		missingVars = append(missingVars, "CROSS_ACCOUNT_KMS_INSTANCE_ID")
	}
	if len(missingVars) > 0 {
		return "", fmt.Errorf("cross-account credentials are configured but required variables are missing: %s; set them to create the cross-account KMS secret in target account %s", strings.Join(missingVars, ", "), rm.remoteAccountID)
	}

	secretName := TestResourcePrefix + "cross-account-kms-secret-" + getRandString()

	log.Printf("Creating KMS secret in target account %s", rm.remoteAccountID)

	createSecretReq := &kms20160120.CreateSecretRequest{}
	createSecretReq.SecretName = tea.String(secretName)
	createSecretReq.VersionId = tea.String("v1")
	createSecretReq.SecretData = tea.String("cross-account-test-secret-data")
	createSecretReq.DKMSInstanceId = tea.String(rm.remoteKMSInstanceID)
	createSecretReq.EncryptionKeyId = tea.String(rm.remoteKMSKeyID)

	_, err := rm.remoteKmsClient.CreateSecret(createSecretReq)
	if err != nil {
		return "", fmt.Errorf("failed to create remote KMS secret: %v", err)
	}

	return secretName, nil
}

// CreateServiceAccount creates service account for tests
func (rm *ResourceManager) CreateServiceAccount() error {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceaccountNameForSAAuth,
			Namespace: ServiceaccountNamespaceForSAAuth.Name,
			Annotations: map[string]string{
				ACKRRSAAnnotation: RAMRoleArnForSAAuth,
			},
		},
	}

	return k8sClient.Create(ctx, serviceAccount)
}

// CleanupAllResources cleans up all created test resources
func (rm *ResourceManager) CleanupAllResources(ctx context.Context) error {
	var errs []error

	// Clean up K8s resources
	err := rm.cleanupK8sResources(ctx)
	if err != nil {
		errs = append(errs, err)
	}

	err = rm.UnbindVPCForDedicatedGateway(ctx, rm.commonKMSInstanceID)
	if err != nil {
		errs = append(errs, err)
	}

	// Clean up KMS resources
	err = rm.deleteKMSSecrets()
	if err != nil {
		errs = append(errs, err)
	}

	// Clean up oos resources
	err = rm.deleteOOSSecret()
	if err != nil {
		errs = append(errs, err)
	}

	// Best-effort fallback sweep of the dedicated watch parameters: warning-
	// only by design, so it never contributes to the aggregated cleanup error.
	rm.sweepOOSSecretParametersByPrefix(oosWatchParamNamePrefix)

	// Clean up RAM roles
	err = rm.deleteRamRoles()
	if err != nil {
		errs = append(errs, err)
	}

	// Clean up RAM users
	err = rm.deleteRamUsers()
	if err != nil {
		errs = append(errs, err)
	}

	// Detach policy from ClusterWorkerRole
	err = rm.detachPolicyForClusterWorkerRole()
	if err != nil {
		errs = append(errs, err)
	}

	// Clean up Ram policys
	err = rm.deleteRamPolicies()
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		// Combine all errors into a single error message
		errorMsg := "cleanup errors occurred:"
		for _, err := range errs {
			errorMsg += "\n- " + err.Error()
		}
		return errors.New(errorMsg)
	}

	return nil
}

// Separate function for cleaning up K8s resources
func (rm *ResourceManager) cleanupK8sResources(ctx context.Context) error {
	if ServiceaccountNamespaceForSAAuth != nil {
		err := k8sClient.Delete(ctx, ServiceaccountNamespaceForSAAuth)
		if err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete namespace %s: %w", ServiceaccountNamespaceForSAAuth.Name, err)
		}
	}

	return nil
}

// deleteKMSSecrets deletes all KMS secrets including template test secrets
func (rm *ResourceManager) deleteKMSSecrets() error {
	// Clean up all KMS secrets including template test secrets
	allSecrets := []string{
		CommonKMSSecretName,
		JsonKMSSecretName,
		YamlKMSSecretName,
		SimpleTemplateSecretName,
		GoTemplateSecretName,
		AuthCredentialsSecretName,
		MergeJsonBaseSecretName,
		MergeJsonOverrideSecretName,
		AdvancedServiceMetadataSecret,
		AdvancedEnvironmentConfigSecret,
		AdvancedDatabaseCredsSecret,
		AdvancedRedisConfigSecret,
		AdvancedLoggingConfigSecret,
		AdvancedAppNameSecret,
		AdvancedAppVersionSecret,
		AdvancedReplicasSecret,
		AdvancedImageRepoSecret,
		AdvancedImageTagSecret,
		AdvancedPortsSecret,
		AdvancedPrivateKeySecret,
		AdvancedCertificateSecret,
		AdvancedCABundleSecret,
		AdvancedKeystorePasswordSecret,
		AdvancedCurrentEnvironmentSecret,
		AdvancedServiceNameSecret,
	}

	for _, secret := range allSecrets {
		if secret == "" {
			continue
		}
		// Delete the KMS secret
		deleteSecretReq := &kms20160120.DeleteSecretRequest{}
		deleteSecretReq.SecretName = tea.String(secret)
		deleteSecretReq.ForceDeleteWithoutRecovery = tea.String("true")
		_, err := rm.kmsClient.DeleteSecret(deleteSecretReq)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return fmt.Errorf("failed to delete KMS secret %s: %w", secret, err)
		}
	}

	// Delete cross-account KMS secret in the target account
	if err := rm.deleteCrossAccountKMSSecret(); err != nil {
		return err
	}

	return nil
}

// deleteCrossAccountKMSSecret deletes the cross-account KMS secret from the target account (Account B).
func (rm *ResourceManager) deleteCrossAccountKMSSecret() error {
	// Symmetric with CreateRemoteKMSCredential: when remote account credentials
	// are not configured the secret is never created and
	// CrossAccountKMSSecretName stays empty, so return early without touching
	// the (nil) remote client. When the name is non-empty, the remote client is
	// guaranteed to be configured.
	if CrossAccountKMSSecretName == "" || rm.remoteKmsClient == nil {
		return nil
	}
	deleteSecretReq := &kms20160120.DeleteSecretRequest{}
	deleteSecretReq.SecretName = tea.String(CrossAccountKMSSecretName)
	deleteSecretReq.ForceDeleteWithoutRecovery = tea.String("true")
	_, err := rm.remoteKmsClient.DeleteSecret(deleteSecretReq)
	if err != nil && !strings.Contains(err.Error(), "Resource not found") {
		return fmt.Errorf("failed to delete cross-account KMS secret %s: %w", CrossAccountKMSSecretName, err)
	}
	return nil
}

// deleteOOSSecret delete oos secret parameter
func (rm *ResourceManager) deleteOOSSecret() error {
	if CommonOOSSecretParameterName == "" {
		return nil
	}
	// Clean up OOS secrets
	deleteSecretParameterReq := &oos20190601.DeleteSecretParameterRequest{}
	deleteSecretParameterReq.Name = tea.String(CommonOOSSecretParameterName)
	_, err := rm.oosClient.DeleteSecretParameter(deleteSecretParameterReq)

	return err
}

func (rm *ResourceManager) deleteRamRoles() error {
	// Delete regular roles in the source account
	for _, role := range []string{RAMRoleNameForRolePlay, RAMRoleNameForRRSA, RAMRoleNameForSAAuth} {
		if role == "" {
			continue
		}
		if err := rm.deleteRamRole(role); err != nil {
			return err
		}
	}

	// Delete cross-account role in the target account
	return rm.deleteCrossAccountRamRole()
}

// deleteCrossAccountRamRole deletes the cross-account RAM role and its KMS policy from the target account (Account B).
func (rm *ResourceManager) deleteCrossAccountRamRole() error {
	// Symmetric with CreateRamRoleForCrossAccount: when remote account
	// credentials are not configured the role is never created and
	// RAMRoleNameForCrossAccount stays empty, so return early without touching
	// the (nil) remote client. When the name is non-empty, the remote client is
	// guaranteed to be configured.
	if RAMRoleNameForCrossAccount == "" || rm.remoteRamClient == nil {
		return nil
	}
	if err := rm.deleteRamRoleWithClient(rm.remoteRamClient, RAMRoleNameForCrossAccount); err != nil {
		return err
	}
	// Also clean up the cross-account KMS policy created in the target account
	if CrossAccountPolicyName != "" {
		deletePolicyReq := &ram20150501.DeletePolicyRequest{PolicyName: tea.String(CrossAccountPolicyName)}
		_, err := rm.remoteRamClient.DeletePolicy(deletePolicyReq)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return err
		}
	}
	return nil
}

func (rm *ResourceManager) deleteRamUsers() error {
	users := map[string]string{
		RAMUserNameForAccessKey: RAMUserAccessKeyID,
		RAMUserNameForRolePlay:  RAMUserAccessKeyIDForRolePlay,
	}

	for user, accessKeyID := range users {
		if user == "" {
			continue
		}
		err := rm.deleteRamUser(user, accessKeyID)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return err
		}
	}

	return nil
}

// deleteRamRole deletes a RAM role in the source account (Account A).
func (rm *ResourceManager) deleteRamRole(roleName string) error {
	return rm.deleteRamRoleWithClient(rm.ramClient, roleName)
}

// deleteRamRoleWithClient deletes a RAM role using the specified client.
func (rm *ResourceManager) deleteRamRoleWithClient(ramClient *ram20150501.Client, roleName string) error {
	// Detach policies first
	listRolePolicys := &ram20150501.ListPoliciesForRoleRequest{}
	listRolePolicys.RoleName = tea.String(roleName)
	policiesResp, err := ramClient.ListPoliciesForRole(listRolePolicys)
	if err != nil {
		return err
	}

	for _, policy := range policiesResp.Body.Policies.Policy {
		detachPolicyReq := &ram20150501.DetachPolicyFromRoleRequest{}
		detachPolicyReq.RoleName = tea.String(roleName)
		detachPolicyReq.PolicyType = policy.PolicyType
		detachPolicyReq.PolicyName = policy.PolicyName
		_, err = ramClient.DetachPolicyFromRole(detachPolicyReq)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return err
		}
	}

	deleteRoleReq := &ram20150501.DeleteRoleRequest{}
	deleteRoleReq.RoleName = tea.String(roleName)
	_, err = ramClient.DeleteRole(deleteRoleReq)
	if err != nil {
		return err
	}

	return nil
}

func (rm *ResourceManager) deleteRamUser(userName, accessKeyID string) error {
	// Detach policies first
	listUserPoliciesReq := &ram20150501.ListPoliciesForUserRequest{}
	listUserPoliciesReq.UserName = tea.String(userName)
	userPoliciesResp, err := rm.ramClient.ListPoliciesForUser(listUserPoliciesReq)
	if err == nil {
		for _, policy := range userPoliciesResp.Body.Policies.Policy {
			detachPolicyReq := &ram20150501.DetachPolicyFromUserRequest{}
			detachPolicyReq.UserName = tea.String(userName)
			detachPolicyReq.PolicyType = policy.PolicyType
			detachPolicyReq.PolicyName = policy.PolicyName
			_, err = rm.ramClient.DetachPolicyFromUser(detachPolicyReq)
			if err != nil {
				return err
			}
		}
	}

	if accessKeyID != "" {
		// Delete the access key
		deleteAccessKeyReq := &ram20150501.DeleteAccessKeyRequest{}
		deleteAccessKeyReq.UserName = tea.String(userName)
		deleteAccessKeyReq.UserAccessKeyId = tea.String(accessKeyID)
		_, err = rm.ramClient.DeleteAccessKey(deleteAccessKeyReq)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return err
		}
	}

	deleteUserReq := &ram20150501.DeleteUserRequest{}
	deleteUserReq.UserName = tea.String(userName)
	_, err = rm.ramClient.DeleteUser(deleteUserReq)
	if err != nil {
		return err
	}

	return nil
}

// Detach policy for cluster worker role
func (rm *ResourceManager) detachPolicyForClusterWorkerRole() error {

	detachPolicyReq := &ram20150501.DetachPolicyFromRoleRequest{}
	detachPolicyReq.RoleName = tea.String(ClusterWorkerRoleName)
	detachPolicyReq.PolicyType = tea.String(PolicyType)
	detachPolicyReq.PolicyName = tea.String(PolicyNameForAccess)
	_, err := rm.ramClient.DetachPolicyFromRole(detachPolicyReq)

	return err
}

func (rm *ResourceManager) deleteRamPolicies() error {
	for _, policyName := range []string{PolicyNameForAccess, PolicyNameForRolePlay} {
		if policyName == "" {
			continue
		}

		// Delete the RAM policy
		deletePolicyReq := &ram20150501.DeletePolicyRequest{}
		deletePolicyReq.PolicyName = tea.String(policyName)
		_, err := rm.ramClient.DeletePolicy(deletePolicyReq)
		if err != nil && !strings.Contains(err.Error(), "Resource not found") {
			return err
		}

	}

	return nil
}

// UnbindVPCForDedicatedGateway unbind VPC to KMS instance
func (rm *ResourceManager) UnbindVPCForDedicatedGateway(ctx context.Context, kmsInstanceId string) error {
	bindVPCReq := &kms20160120.UpdateKmsInstanceBindVpcRequest{}
	bindVPCReq.KmsInstanceId = tea.String(kmsInstanceId)
	bindVPCReq.BindVpcs = tea.String("[]")

	_, err := rm.kmsClient.UpdateKmsInstanceBindVpc(bindVPCReq)

	return err
}
