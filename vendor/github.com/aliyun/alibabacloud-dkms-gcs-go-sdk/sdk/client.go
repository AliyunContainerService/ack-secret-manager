// This file is auto-generated, don't edit it. Thanks.
package client

import (
	encodeutil "github.com/alibabacloud-go/darabonba-encode-util/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	dedicatedkmsopenapi "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi"
	dedicatedkmsopenapiutil "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util"
)

type EncryptRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 对数据加密时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s EncryptRequest) String() string {
	return tea.Prettify(s)
}

func (s EncryptRequest) GoString() string {
	return s.String()
}

func (s *EncryptRequest) SetKeyId(v string) *EncryptRequest {
	s.KeyId = &v
	return s
}

func (s *EncryptRequest) SetPlaintext(v []byte) *EncryptRequest {
	s.Plaintext = v
	return s
}

func (s *EncryptRequest) SetAlgorithm(v string) *EncryptRequest {
	s.Algorithm = &v
	return s
}

func (s *EncryptRequest) SetAad(v []byte) *EncryptRequest {
	s.Aad = v
	return s
}

func (s *EncryptRequest) SetIv(v []byte) *EncryptRequest {
	s.Iv = v
	return s
}

func (s *EncryptRequest) SetPaddingMode(v string) *EncryptRequest {
	s.PaddingMode = &v
	return s
}

func (s *EncryptRequest) SetHeaders(v map[string]*string) *EncryptRequest {
	s.Headers = v
	return s
}

type EncryptResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s EncryptResponse) String() string {
	return tea.Prettify(s)
}

func (s EncryptResponse) GoString() string {
	return s.String()
}

func (s *EncryptResponse) SetKeyId(v string) *EncryptResponse {
	s.KeyId = &v
	return s
}

func (s *EncryptResponse) SetCiphertextBlob(v []byte) *EncryptResponse {
	s.CiphertextBlob = v
	return s
}

func (s *EncryptResponse) SetIv(v []byte) *EncryptResponse {
	s.Iv = v
	return s
}

func (s *EncryptResponse) SetRequestId(v string) *EncryptResponse {
	s.RequestId = &v
	return s
}

func (s *EncryptResponse) SetAlgorithm(v string) *EncryptResponse {
	s.Algorithm = &v
	return s
}

func (s *EncryptResponse) SetPaddingMode(v string) *EncryptResponse {
	s.PaddingMode = &v
	return s
}

func (s *EncryptResponse) SetHeaders(v map[string]*string) *EncryptResponse {
	s.Headers = v
	return s
}

type DecryptRequest struct {
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s DecryptRequest) String() string {
	return tea.Prettify(s)
}

func (s DecryptRequest) GoString() string {
	return s.String()
}

func (s *DecryptRequest) SetCiphertextBlob(v []byte) *DecryptRequest {
	s.CiphertextBlob = v
	return s
}

func (s *DecryptRequest) SetKeyId(v string) *DecryptRequest {
	s.KeyId = &v
	return s
}

func (s *DecryptRequest) SetAlgorithm(v string) *DecryptRequest {
	s.Algorithm = &v
	return s
}

func (s *DecryptRequest) SetAad(v []byte) *DecryptRequest {
	s.Aad = v
	return s
}

func (s *DecryptRequest) SetIv(v []byte) *DecryptRequest {
	s.Iv = v
	return s
}

func (s *DecryptRequest) SetPaddingMode(v string) *DecryptRequest {
	s.PaddingMode = &v
	return s
}

func (s *DecryptRequest) SetHeaders(v map[string]*string) *DecryptRequest {
	s.Headers = v
	return s
}

type DecryptResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s DecryptResponse) String() string {
	return tea.Prettify(s)
}

func (s DecryptResponse) GoString() string {
	return s.String()
}

func (s *DecryptResponse) SetKeyId(v string) *DecryptResponse {
	s.KeyId = &v
	return s
}

func (s *DecryptResponse) SetPlaintext(v []byte) *DecryptResponse {
	s.Plaintext = v
	return s
}

func (s *DecryptResponse) SetRequestId(v string) *DecryptResponse {
	s.RequestId = &v
	return s
}

func (s *DecryptResponse) SetAlgorithm(v string) *DecryptResponse {
	s.Algorithm = &v
	return s
}

func (s *DecryptResponse) SetPaddingMode(v string) *DecryptResponse {
	s.PaddingMode = &v
	return s
}

func (s *DecryptResponse) SetHeaders(v map[string]*string) *DecryptResponse {
	s.Headers = v
	return s
}

type SignRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 签名消息
	Message []byte `json:"Message,omitempty" xml:"Message,omitempty"`
	// 消息类型: 1. RAW（默认值）：原始数据2. DIGEST：原始数据的消息摘要
	MessageType *string `json:"MessageType,omitempty" xml:"MessageType,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s SignRequest) String() string {
	return tea.Prettify(s)
}

func (s SignRequest) GoString() string {
	return s.String()
}

func (s *SignRequest) SetKeyId(v string) *SignRequest {
	s.KeyId = &v
	return s
}

func (s *SignRequest) SetAlgorithm(v string) *SignRequest {
	s.Algorithm = &v
	return s
}

func (s *SignRequest) SetMessage(v []byte) *SignRequest {
	s.Message = v
	return s
}

func (s *SignRequest) SetMessageType(v string) *SignRequest {
	s.MessageType = &v
	return s
}

func (s *SignRequest) SetHeaders(v map[string]*string) *SignRequest {
	s.Headers = v
	return s
}

type SignResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 计算出来的签名值
	Signature []byte `json:"Signature,omitempty" xml:"Signature,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 消息类型: 1. RAW（默认值）：原始数据2. DIGEST：原始数据的消息摘要
	MessageType *string `json:"MessageType,omitempty" xml:"MessageType,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s SignResponse) String() string {
	return tea.Prettify(s)
}

func (s SignResponse) GoString() string {
	return s.String()
}

func (s *SignResponse) SetKeyId(v string) *SignResponse {
	s.KeyId = &v
	return s
}

func (s *SignResponse) SetSignature(v []byte) *SignResponse {
	s.Signature = v
	return s
}

func (s *SignResponse) SetRequestId(v string) *SignResponse {
	s.RequestId = &v
	return s
}

func (s *SignResponse) SetAlgorithm(v string) *SignResponse {
	s.Algorithm = &v
	return s
}

func (s *SignResponse) SetMessageType(v string) *SignResponse {
	s.MessageType = &v
	return s
}

func (s *SignResponse) SetHeaders(v map[string]*string) *SignResponse {
	s.Headers = v
	return s
}

type VerifyRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 计算出来的签名值
	Signature []byte `json:"Signature,omitempty" xml:"Signature,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 签名消息
	Message []byte `json:"Message,omitempty" xml:"Message,omitempty"`
	// 消息类型: 1. RAW（默认值）：原始数据2. DIGEST：原始数据的消息摘要
	MessageType *string `json:"MessageType,omitempty" xml:"MessageType,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s VerifyRequest) String() string {
	return tea.Prettify(s)
}

func (s VerifyRequest) GoString() string {
	return s.String()
}

func (s *VerifyRequest) SetKeyId(v string) *VerifyRequest {
	s.KeyId = &v
	return s
}

func (s *VerifyRequest) SetSignature(v []byte) *VerifyRequest {
	s.Signature = v
	return s
}

func (s *VerifyRequest) SetAlgorithm(v string) *VerifyRequest {
	s.Algorithm = &v
	return s
}

func (s *VerifyRequest) SetMessage(v []byte) *VerifyRequest {
	s.Message = v
	return s
}

func (s *VerifyRequest) SetMessageType(v string) *VerifyRequest {
	s.MessageType = &v
	return s
}

func (s *VerifyRequest) SetHeaders(v map[string]*string) *VerifyRequest {
	s.Headers = v
	return s
}

type VerifyResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 签名验证是否通过
	Value *bool `json:"Value,omitempty" xml:"Value,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 消息类型: 1. RAW（默认值）：原始数据2. DIGEST：原始数据的消息摘要
	MessageType *string `json:"MessageType,omitempty" xml:"MessageType,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s VerifyResponse) String() string {
	return tea.Prettify(s)
}

func (s VerifyResponse) GoString() string {
	return s.String()
}

func (s *VerifyResponse) SetKeyId(v string) *VerifyResponse {
	s.KeyId = &v
	return s
}

func (s *VerifyResponse) SetValue(v bool) *VerifyResponse {
	s.Value = &v
	return s
}

func (s *VerifyResponse) SetRequestId(v string) *VerifyResponse {
	s.RequestId = &v
	return s
}

func (s *VerifyResponse) SetAlgorithm(v string) *VerifyResponse {
	s.Algorithm = &v
	return s
}

func (s *VerifyResponse) SetMessageType(v string) *VerifyResponse {
	s.MessageType = &v
	return s
}

func (s *VerifyResponse) SetHeaders(v map[string]*string) *VerifyResponse {
	s.Headers = v
	return s
}

type GenerateRandomRequest struct {
	// 要生成的随机数字节长度
	Length *int32 `json:"Length,omitempty" xml:"Length,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GenerateRandomRequest) String() string {
	return tea.Prettify(s)
}

func (s GenerateRandomRequest) GoString() string {
	return s.String()
}

func (s *GenerateRandomRequest) SetLength(v int32) *GenerateRandomRequest {
	s.Length = &v
	return s
}

func (s *GenerateRandomRequest) SetHeaders(v map[string]*string) *GenerateRandomRequest {
	s.Headers = v
	return s
}

type GenerateRandomResponse struct {
	// 随机数
	Random []byte `json:"Random,omitempty" xml:"Random,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GenerateRandomResponse) String() string {
	return tea.Prettify(s)
}

func (s GenerateRandomResponse) GoString() string {
	return s.String()
}

func (s *GenerateRandomResponse) SetRandom(v []byte) *GenerateRandomResponse {
	s.Random = v
	return s
}

func (s *GenerateRandomResponse) SetRequestId(v string) *GenerateRandomResponse {
	s.RequestId = &v
	return s
}

func (s *GenerateRandomResponse) SetHeaders(v map[string]*string) *GenerateRandomResponse {
	s.Headers = v
	return s
}

type GenerateDataKeyRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 生成的数据密钥的长度
	NumberOfBytes *int32 `json:"NumberOfBytes,omitempty" xml:"NumberOfBytes,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GenerateDataKeyRequest) String() string {
	return tea.Prettify(s)
}

func (s GenerateDataKeyRequest) GoString() string {
	return s.String()
}

func (s *GenerateDataKeyRequest) SetKeyId(v string) *GenerateDataKeyRequest {
	s.KeyId = &v
	return s
}

func (s *GenerateDataKeyRequest) SetAlgorithm(v string) *GenerateDataKeyRequest {
	s.Algorithm = &v
	return s
}

func (s *GenerateDataKeyRequest) SetNumberOfBytes(v int32) *GenerateDataKeyRequest {
	s.NumberOfBytes = &v
	return s
}

func (s *GenerateDataKeyRequest) SetAad(v []byte) *GenerateDataKeyRequest {
	s.Aad = v
	return s
}

func (s *GenerateDataKeyRequest) SetHeaders(v map[string]*string) *GenerateDataKeyRequest {
	s.Headers = v
	return s
}

type GenerateDataKeyResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GenerateDataKeyResponse) String() string {
	return tea.Prettify(s)
}

func (s GenerateDataKeyResponse) GoString() string {
	return s.String()
}

func (s *GenerateDataKeyResponse) SetKeyId(v string) *GenerateDataKeyResponse {
	s.KeyId = &v
	return s
}

func (s *GenerateDataKeyResponse) SetIv(v []byte) *GenerateDataKeyResponse {
	s.Iv = v
	return s
}

func (s *GenerateDataKeyResponse) SetPlaintext(v []byte) *GenerateDataKeyResponse {
	s.Plaintext = v
	return s
}

func (s *GenerateDataKeyResponse) SetCiphertextBlob(v []byte) *GenerateDataKeyResponse {
	s.CiphertextBlob = v
	return s
}

func (s *GenerateDataKeyResponse) SetRequestId(v string) *GenerateDataKeyResponse {
	s.RequestId = &v
	return s
}

func (s *GenerateDataKeyResponse) SetAlgorithm(v string) *GenerateDataKeyResponse {
	s.Algorithm = &v
	return s
}

func (s *GenerateDataKeyResponse) SetHeaders(v map[string]*string) *GenerateDataKeyResponse {
	s.Headers = v
	return s
}

type GetPublicKeyRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GetPublicKeyRequest) String() string {
	return tea.Prettify(s)
}

func (s GetPublicKeyRequest) GoString() string {
	return s.String()
}

func (s *GetPublicKeyRequest) SetKeyId(v string) *GetPublicKeyRequest {
	s.KeyId = &v
	return s
}

func (s *GetPublicKeyRequest) SetHeaders(v map[string]*string) *GetPublicKeyRequest {
	s.Headers = v
	return s
}

type GetPublicKeyResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// PEM格式的公钥
	PublicKey *string `json:"PublicKey,omitempty" xml:"PublicKey,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GetPublicKeyResponse) String() string {
	return tea.Prettify(s)
}

func (s GetPublicKeyResponse) GoString() string {
	return s.String()
}

func (s *GetPublicKeyResponse) SetKeyId(v string) *GetPublicKeyResponse {
	s.KeyId = &v
	return s
}

func (s *GetPublicKeyResponse) SetPublicKey(v string) *GetPublicKeyResponse {
	s.PublicKey = &v
	return s
}

func (s *GetPublicKeyResponse) SetRequestId(v string) *GetPublicKeyResponse {
	s.RequestId = &v
	return s
}

func (s *GetPublicKeyResponse) SetHeaders(v map[string]*string) *GetPublicKeyResponse {
	s.Headers = v
	return s
}

type GetSecretValueRequest struct {
	// 凭据名称
	SecretName *string `json:"SecretName,omitempty" xml:"SecretName,omitempty"`
	// 版本状态
	VersionStage *string `json:"VersionStage,omitempty" xml:"VersionStage,omitempty"`
	// 版本号
	VersionId *string `json:"VersionId,omitempty" xml:"VersionId,omitempty"`
	// 是否获取凭据的拓展配置true（默认值）：是,false：否
	FetchExtendedConfig *bool `json:"FetchExtendedConfig,omitempty" xml:"FetchExtendedConfig,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GetSecretValueRequest) String() string {
	return tea.Prettify(s)
}

func (s GetSecretValueRequest) GoString() string {
	return s.String()
}

func (s *GetSecretValueRequest) SetSecretName(v string) *GetSecretValueRequest {
	s.SecretName = &v
	return s
}

func (s *GetSecretValueRequest) SetVersionStage(v string) *GetSecretValueRequest {
	s.VersionStage = &v
	return s
}

func (s *GetSecretValueRequest) SetVersionId(v string) *GetSecretValueRequest {
	s.VersionId = &v
	return s
}

func (s *GetSecretValueRequest) SetFetchExtendedConfig(v bool) *GetSecretValueRequest {
	s.FetchExtendedConfig = &v
	return s
}

func (s *GetSecretValueRequest) SetHeaders(v map[string]*string) *GetSecretValueRequest {
	s.Headers = v
	return s
}

type GetSecretValueResponse struct {
	// 凭据名称
	SecretName *string `json:"SecretName,omitempty" xml:"SecretName,omitempty"`
	// 凭据类型
	SecretType *string `json:"SecretType,omitempty" xml:"SecretType,omitempty"`
	// 凭据值
	SecretData *string `json:"SecretData,omitempty" xml:"SecretData,omitempty"`
	// 凭据值类型
	SecretDataType *string `json:"SecretDataType,omitempty" xml:"SecretDataType,omitempty"`
	// 凭据版本的状态标记
	VersionStages []*string `json:"VersionStages,omitempty" xml:"VersionStages,omitempty" type:"Repeated"`
	// 凭据版本的标识符
	VersionId *string `json:"VersionId,omitempty" xml:"VersionId,omitempty"`
	// 创建凭据的时间
	CreateTime *string `json:"CreateTime,omitempty" xml:"CreateTime,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 最近一次轮转的时间
	LastRotationDate *string `json:"LastRotationDate,omitempty" xml:"LastRotationDate,omitempty"`
	// 下一次轮转的时间
	NextRotationDate *string `json:"NextRotationDate,omitempty" xml:"NextRotationDate,omitempty"`
	// 凭据的拓展配置
	ExtendedConfig *string `json:"ExtendedConfig,omitempty" xml:"ExtendedConfig,omitempty"`
	// 是否开启自动轮转
	AutomaticRotation *string `json:"AutomaticRotation,omitempty" xml:"AutomaticRotation,omitempty"`
	// 凭据自动轮转的周期
	RotationInterval *string `json:"RotationInterval,omitempty" xml:"RotationInterval,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s GetSecretValueResponse) String() string {
	return tea.Prettify(s)
}

func (s GetSecretValueResponse) GoString() string {
	return s.String()
}

func (s *GetSecretValueResponse) SetSecretName(v string) *GetSecretValueResponse {
	s.SecretName = &v
	return s
}

func (s *GetSecretValueResponse) SetSecretType(v string) *GetSecretValueResponse {
	s.SecretType = &v
	return s
}

func (s *GetSecretValueResponse) SetSecretData(v string) *GetSecretValueResponse {
	s.SecretData = &v
	return s
}

func (s *GetSecretValueResponse) SetSecretDataType(v string) *GetSecretValueResponse {
	s.SecretDataType = &v
	return s
}

func (s *GetSecretValueResponse) SetVersionStages(v []*string) *GetSecretValueResponse {
	s.VersionStages = v
	return s
}

func (s *GetSecretValueResponse) SetVersionId(v string) *GetSecretValueResponse {
	s.VersionId = &v
	return s
}

func (s *GetSecretValueResponse) SetCreateTime(v string) *GetSecretValueResponse {
	s.CreateTime = &v
	return s
}

func (s *GetSecretValueResponse) SetRequestId(v string) *GetSecretValueResponse {
	s.RequestId = &v
	return s
}

func (s *GetSecretValueResponse) SetLastRotationDate(v string) *GetSecretValueResponse {
	s.LastRotationDate = &v
	return s
}

func (s *GetSecretValueResponse) SetNextRotationDate(v string) *GetSecretValueResponse {
	s.NextRotationDate = &v
	return s
}

func (s *GetSecretValueResponse) SetExtendedConfig(v string) *GetSecretValueResponse {
	s.ExtendedConfig = &v
	return s
}

func (s *GetSecretValueResponse) SetAutomaticRotation(v string) *GetSecretValueResponse {
	s.AutomaticRotation = &v
	return s
}

func (s *GetSecretValueResponse) SetRotationInterval(v string) *GetSecretValueResponse {
	s.RotationInterval = &v
	return s
}

func (s *GetSecretValueResponse) SetHeaders(v map[string]*string) *GetSecretValueResponse {
	s.Headers = v
	return s
}

type AdvanceEncryptRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceEncryptRequest) String() string {
	return tea.Prettify(s)
}

func (s AdvanceEncryptRequest) GoString() string {
	return s.String()
}

func (s *AdvanceEncryptRequest) SetKeyId(v string) *AdvanceEncryptRequest {
	s.KeyId = &v
	return s
}

func (s *AdvanceEncryptRequest) SetPlaintext(v []byte) *AdvanceEncryptRequest {
	s.Plaintext = v
	return s
}

func (s *AdvanceEncryptRequest) SetAlgorithm(v string) *AdvanceEncryptRequest {
	s.Algorithm = &v
	return s
}

func (s *AdvanceEncryptRequest) SetAad(v []byte) *AdvanceEncryptRequest {
	s.Aad = v
	return s
}

func (s *AdvanceEncryptRequest) SetIv(v []byte) *AdvanceEncryptRequest {
	s.Iv = v
	return s
}

func (s *AdvanceEncryptRequest) SetPaddingMode(v string) *AdvanceEncryptRequest {
	s.PaddingMode = &v
	return s
}

func (s *AdvanceEncryptRequest) SetHeaders(v map[string]*string) *AdvanceEncryptRequest {
	s.Headers = v
	return s
}

type AdvanceEncryptResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 密钥版本唯一标识符
	KeyVersionId *string `json:"KeyVersionId,omitempty" xml:"KeyVersionId,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceEncryptResponse) String() string {
	return tea.Prettify(s)
}

func (s AdvanceEncryptResponse) GoString() string {
	return s.String()
}

func (s *AdvanceEncryptResponse) SetKeyId(v string) *AdvanceEncryptResponse {
	s.KeyId = &v
	return s
}

func (s *AdvanceEncryptResponse) SetCiphertextBlob(v []byte) *AdvanceEncryptResponse {
	s.CiphertextBlob = v
	return s
}

func (s *AdvanceEncryptResponse) SetIv(v []byte) *AdvanceEncryptResponse {
	s.Iv = v
	return s
}

func (s *AdvanceEncryptResponse) SetRequestId(v string) *AdvanceEncryptResponse {
	s.RequestId = &v
	return s
}

func (s *AdvanceEncryptResponse) SetAlgorithm(v string) *AdvanceEncryptResponse {
	s.Algorithm = &v
	return s
}

func (s *AdvanceEncryptResponse) SetPaddingMode(v string) *AdvanceEncryptResponse {
	s.PaddingMode = &v
	return s
}

func (s *AdvanceEncryptResponse) SetKeyVersionId(v string) *AdvanceEncryptResponse {
	s.KeyVersionId = &v
	return s
}

func (s *AdvanceEncryptResponse) SetHeaders(v map[string]*string) *AdvanceEncryptResponse {
	s.Headers = v
	return s
}

type AdvanceDecryptRequest struct {
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceDecryptRequest) String() string {
	return tea.Prettify(s)
}

func (s AdvanceDecryptRequest) GoString() string {
	return s.String()
}

func (s *AdvanceDecryptRequest) SetCiphertextBlob(v []byte) *AdvanceDecryptRequest {
	s.CiphertextBlob = v
	return s
}

func (s *AdvanceDecryptRequest) SetKeyId(v string) *AdvanceDecryptRequest {
	s.KeyId = &v
	return s
}

func (s *AdvanceDecryptRequest) SetAlgorithm(v string) *AdvanceDecryptRequest {
	s.Algorithm = &v
	return s
}

func (s *AdvanceDecryptRequest) SetAad(v []byte) *AdvanceDecryptRequest {
	s.Aad = v
	return s
}

func (s *AdvanceDecryptRequest) SetIv(v []byte) *AdvanceDecryptRequest {
	s.Iv = v
	return s
}

func (s *AdvanceDecryptRequest) SetPaddingMode(v string) *AdvanceDecryptRequest {
	s.PaddingMode = &v
	return s
}

func (s *AdvanceDecryptRequest) SetHeaders(v map[string]*string) *AdvanceDecryptRequest {
	s.Headers = v
	return s
}

type AdvanceDecryptResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 填充模式
	PaddingMode *string `json:"PaddingMode,omitempty" xml:"PaddingMode,omitempty"`
	// 密钥版本唯一标识符
	KeyVersionId *string `json:"KeyVersionId,omitempty" xml:"KeyVersionId,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceDecryptResponse) String() string {
	return tea.Prettify(s)
}

func (s AdvanceDecryptResponse) GoString() string {
	return s.String()
}

func (s *AdvanceDecryptResponse) SetKeyId(v string) *AdvanceDecryptResponse {
	s.KeyId = &v
	return s
}

func (s *AdvanceDecryptResponse) SetPlaintext(v []byte) *AdvanceDecryptResponse {
	s.Plaintext = v
	return s
}

func (s *AdvanceDecryptResponse) SetRequestId(v string) *AdvanceDecryptResponse {
	s.RequestId = &v
	return s
}

func (s *AdvanceDecryptResponse) SetAlgorithm(v string) *AdvanceDecryptResponse {
	s.Algorithm = &v
	return s
}

func (s *AdvanceDecryptResponse) SetPaddingMode(v string) *AdvanceDecryptResponse {
	s.PaddingMode = &v
	return s
}

func (s *AdvanceDecryptResponse) SetKeyVersionId(v string) *AdvanceDecryptResponse {
	s.KeyVersionId = &v
	return s
}

func (s *AdvanceDecryptResponse) SetHeaders(v map[string]*string) *AdvanceDecryptResponse {
	s.Headers = v
	return s
}

type AdvanceGenerateDataKeyRequest struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 生成的数据密钥的长度
	NumberOfBytes *int32 `json:"NumberOfBytes,omitempty" xml:"NumberOfBytes,omitempty"`
	// 对数据密钥加密时使用的GCM加密模式认证数据
	Aad []byte `json:"Aad,omitempty" xml:"Aad,omitempty"`
	// 请求头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceGenerateDataKeyRequest) String() string {
	return tea.Prettify(s)
}

func (s AdvanceGenerateDataKeyRequest) GoString() string {
	return s.String()
}

func (s *AdvanceGenerateDataKeyRequest) SetKeyId(v string) *AdvanceGenerateDataKeyRequest {
	s.KeyId = &v
	return s
}

func (s *AdvanceGenerateDataKeyRequest) SetNumberOfBytes(v int32) *AdvanceGenerateDataKeyRequest {
	s.NumberOfBytes = &v
	return s
}

func (s *AdvanceGenerateDataKeyRequest) SetAad(v []byte) *AdvanceGenerateDataKeyRequest {
	s.Aad = v
	return s
}

func (s *AdvanceGenerateDataKeyRequest) SetHeaders(v map[string]*string) *AdvanceGenerateDataKeyRequest {
	s.Headers = v
	return s
}

type AdvanceGenerateDataKeyResponse struct {
	// 密钥的全局唯一标识符该参数也可以被指定为密钥别名
	KeyId *string `json:"KeyId,omitempty" xml:"KeyId,omitempty"`
	// 加密数据时使用的初始向量
	Iv []byte `json:"Iv,omitempty" xml:"Iv,omitempty"`
	// 待加密的明文数据
	Plaintext []byte `json:"Plaintext,omitempty" xml:"Plaintext,omitempty"`
	// 数据被指定密钥加密后的密文
	CiphertextBlob []byte `json:"CiphertextBlob,omitempty" xml:"CiphertextBlob,omitempty"`
	// 请求ID
	RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty"`
	// 加密算法
	Algorithm *string `json:"Algorithm,omitempty" xml:"Algorithm,omitempty"`
	// 密钥版本唯一标识符
	KeyVersionId *string `json:"KeyVersionId,omitempty" xml:"KeyVersionId,omitempty"`
	// 响应头
	Headers map[string]*string `json:"headers,omitempty" xml:"headers,omitempty"`
}

func (s AdvanceGenerateDataKeyResponse) String() string {
	return tea.Prettify(s)
}

func (s AdvanceGenerateDataKeyResponse) GoString() string {
	return s.String()
}

func (s *AdvanceGenerateDataKeyResponse) SetKeyId(v string) *AdvanceGenerateDataKeyResponse {
	s.KeyId = &v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetIv(v []byte) *AdvanceGenerateDataKeyResponse {
	s.Iv = v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetPlaintext(v []byte) *AdvanceGenerateDataKeyResponse {
	s.Plaintext = v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetCiphertextBlob(v []byte) *AdvanceGenerateDataKeyResponse {
	s.CiphertextBlob = v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetRequestId(v string) *AdvanceGenerateDataKeyResponse {
	s.RequestId = &v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetAlgorithm(v string) *AdvanceGenerateDataKeyResponse {
	s.Algorithm = &v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetKeyVersionId(v string) *AdvanceGenerateDataKeyResponse {
	s.KeyVersionId = &v
	return s
}

func (s *AdvanceGenerateDataKeyResponse) SetHeaders(v map[string]*string) *AdvanceGenerateDataKeyResponse {
	s.Headers = v
	return s
}

type Client struct {
	dedicatedkmsopenapi.Client
}

func NewClient(config *dedicatedkmsopenapi.Config) (*Client, error) {
	client := new(Client)
	err := client.Init(config)
	return client, err
}

func (client *Client) Init(config *dedicatedkmsopenapi.Config) (_err error) {
	_err = client.Client.Init(config)
	if _err != nil {
		return _err
	}
	return nil
}

/**
 * 调用Encrypt接口将明文加密为密文
 * @param request
 * @return EncryptResponse
 */
func (client *Client) Encrypt(request *EncryptRequest) (_result *EncryptResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &EncryptResponse{}
	_body, _err := client.EncryptWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用Encrypt接口将明文加密为密文
 * @param request
 * @param runtime
 * @return EncryptResponse
 */
func (client *Client) EncryptWithOptions(request *EncryptRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *EncryptResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedEncryptRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("Encrypt"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseEncryptResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &EncryptResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":          respMap["KeyId"],
		"CiphertextBlob": respMap["CiphertextBlob"],
		"Iv":             respMap["Iv"],
		"RequestId":      respMap["RequestId"],
		"Algorithm":      respMap["Algorithm"],
		"PaddingMode":    respMap["PaddingMode"],
		"headers":        responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用Decrypt接口将密文解密为明文
 * @param request
 * @return DecryptResponse
 */
func (client *Client) Decrypt(request *DecryptRequest) (_result *DecryptResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &DecryptResponse{}
	_body, _err := client.DecryptWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用Decrypt接口将密文解密为明文
 * @param request
 * @param runtime
 * @return DecryptResponse
 */
func (client *Client) DecryptWithOptions(request *DecryptRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *DecryptResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedDecryptRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("Decrypt"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseDecryptResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &DecryptResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":       respMap["KeyId"],
		"Plaintext":   respMap["Plaintext"],
		"RequestId":   respMap["RequestId"],
		"Algorithm":   respMap["Algorithm"],
		"PaddingMode": respMap["PaddingMode"],
		"headers":     responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用Sign接口使用非对称密钥进行签名
 * @param request
 * @return SignResponse
 */
func (client *Client) Sign(request *SignRequest) (_result *SignResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &SignResponse{}
	_body, _err := client.SignWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用Sign接口使用非对称密钥进行签名
 * @param request
 * @param runtime
 * @return SignResponse
 */
func (client *Client) SignWithOptions(request *SignRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *SignResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedSignRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("Sign"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseSignResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &SignResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":       respMap["KeyId"],
		"Signature":   respMap["Signature"],
		"RequestId":   respMap["RequestId"],
		"Algorithm":   respMap["Algorithm"],
		"MessageType": respMap["MessageType"],
		"headers":     responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用Verify接口使用非对称密钥进行验签
 * @param request
 * @return VerifyResponse
 */
func (client *Client) Verify(request *VerifyRequest) (_result *VerifyResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &VerifyResponse{}
	_body, _err := client.VerifyWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用Verify接口使用非对称密钥进行验签
 * @param request
 * @param runtime
 * @return VerifyResponse
 */
func (client *Client) VerifyWithOptions(request *VerifyRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *VerifyResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedVerifyRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("Verify"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseVerifyResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &VerifyResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":       respMap["KeyId"],
		"Value":       respMap["Value"],
		"RequestId":   respMap["RequestId"],
		"Algorithm":   respMap["Algorithm"],
		"MessageType": respMap["MessageType"],
		"headers":     responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用GenerateRandom接口生成一个随机数
 * @param request
 * @return GenerateRandomResponse
 */
func (client *Client) GenerateRandom(request *GenerateRandomRequest) (_result *GenerateRandomResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &GenerateRandomResponse{}
	_body, _err := client.GenerateRandomWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用GenerateRandom接口生成一个随机数
 * @param request
 * @param runtime
 * @return GenerateRandomResponse
 */
func (client *Client) GenerateRandomWithOptions(request *GenerateRandomRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *GenerateRandomResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedGenerateRandomRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("GenerateRandom"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseGenerateRandomResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &GenerateRandomResponse{}
	_err = tea.Convert(map[string]interface{}{
		"Random":    respMap["Random"],
		"RequestId": respMap["RequestId"],
		"headers":   responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用GenerateDataKey接口生成数据密钥
 * @param request
 * @return GenerateDataKeyResponse
 */
func (client *Client) GenerateDataKey(request *GenerateDataKeyRequest) (_result *GenerateDataKeyResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &GenerateDataKeyResponse{}
	_body, _err := client.GenerateDataKeyWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用GenerateDataKey接口生成数据密钥
 * @param request
 * @param runtime
 * @return GenerateDataKeyResponse
 */
func (client *Client) GenerateDataKeyWithOptions(request *GenerateDataKeyRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *GenerateDataKeyResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedGenerateDataKeyRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("GenerateDataKey"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseGenerateDataKeyResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &GenerateDataKeyResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":          respMap["KeyId"],
		"Iv":             respMap["Iv"],
		"Plaintext":      respMap["Plaintext"],
		"CiphertextBlob": respMap["CiphertextBlob"],
		"RequestId":      respMap["RequestId"],
		"Algorithm":      respMap["Algorithm"],
		"headers":        responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用GetPublicKey接口获取指定非对称密钥的公钥
 * @param request
 * @return GetPublicKeyResponse
 */
func (client *Client) GetPublicKey(request *GetPublicKeyRequest) (_result *GetPublicKeyResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &GetPublicKeyResponse{}
	_body, _err := client.GetPublicKeyWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用GetPublicKey接口获取指定非对称密钥的公钥
 * @param request
 * @param runtime
 * @return GetPublicKeyResponse
 */
func (client *Client) GetPublicKeyWithOptions(request *GetPublicKeyRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *GetPublicKeyResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedGetPublicKeyRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("GetPublicKey"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseGetPublicKeyResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &GetPublicKeyResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":     respMap["KeyId"],
		"PublicKey": respMap["PublicKey"],
		"RequestId": respMap["RequestId"],
		"headers":   responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用GetSecretValue接口通过KMS实例网关获取凭据值
 * @param request
 * @return GetSecretValueResponse
 */
func (client *Client) GetSecretValue(request *GetSecretValueRequest) (_result *GetSecretValueResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &GetSecretValueResponse{}
	_body, _err := client.GetSecretValueWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用GetSecretValue接口通过KMS实例网关获取凭据值
 * @param request
 * @param runtime
 * @return GetSecretValueResponse
 */
func (client *Client) GetSecretValueWithOptions(request *GetSecretValueRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *GetSecretValueResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedGetSecretValueRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("GetSecretValue"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseGetSecretValueResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &GetSecretValueResponse{}
	_err = tea.Convert(map[string]interface{}{
		"SecretName":        respMap["SecretName"],
		"SecretType":        respMap["SecretType"],
		"SecretData":        respMap["SecretData"],
		"SecretDataType":    respMap["SecretDataType"],
		"VersionStages":     respMap["VersionStages"],
		"VersionId":         respMap["VersionId"],
		"CreateTime":        respMap["CreateTime"],
		"RequestId":         respMap["RequestId"],
		"LastRotationDate":  respMap["LastRotationDate"],
		"NextRotationDate":  respMap["NextRotationDate"],
		"ExtendedConfig":    respMap["ExtendedConfig"],
		"AutomaticRotation": respMap["AutomaticRotation"],
		"RotationInterval":  respMap["RotationInterval"],
		"headers":           responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用AdvanceEncrypt接口将明文高级加密为密文
 * @param request
 * @return AdvanceEncryptResponse
 */
func (client *Client) AdvanceEncrypt(request *AdvanceEncryptRequest) (_result *AdvanceEncryptResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &AdvanceEncryptResponse{}
	_body, _err := client.AdvanceEncryptWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用AdvanceEncrypt接口将明文高级加密为密文
 * @param request
 * @param runtime
 * @return AdvanceEncryptResponse
 */
func (client *Client) AdvanceEncryptWithOptions(request *AdvanceEncryptRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *AdvanceEncryptResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedAdvanceEncryptRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("AdvanceEncrypt"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseAdvanceEncryptResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &AdvanceEncryptResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":          respMap["KeyId"],
		"CiphertextBlob": respMap["CiphertextBlob"],
		"Iv":             respMap["Iv"],
		"RequestId":      respMap["RequestId"],
		"Algorithm":      respMap["Algorithm"],
		"PaddingMode":    respMap["PaddingMode"],
		"KeyVersionId":   respMap["KeyVersionId"],
		"headers":        responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用AdvanceDecrypt接口将密文高级解密为明文
 * @param request
 * @return AdvanceDecryptResponse
 */
func (client *Client) AdvanceDecrypt(request *AdvanceDecryptRequest) (_result *AdvanceDecryptResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &AdvanceDecryptResponse{}
	_body, _err := client.AdvanceDecryptWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用AdvanceDecrypt接口将密文高级解密为明文
 * @param request
 * @param runtime
 * @return AdvanceDecryptResponse
 */
func (client *Client) AdvanceDecryptWithOptions(request *AdvanceDecryptRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *AdvanceDecryptResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedAdvanceDecryptRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("AdvanceDecrypt"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseAdvanceDecryptResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &AdvanceDecryptResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":        respMap["KeyId"],
		"Plaintext":    respMap["Plaintext"],
		"RequestId":    respMap["RequestId"],
		"Algorithm":    respMap["Algorithm"],
		"PaddingMode":  respMap["PaddingMode"],
		"KeyVersionId": respMap["KeyVersionId"],
		"headers":      responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}

/**
 * 调用AdvanceGenerateDataKey接口高级生成数据密钥
 * @param request
 * @return AdvanceGenerateDataKeyRequest
 */
func (client *Client) AdvanceGenerateDataKey(request *AdvanceGenerateDataKeyRequest) (_result *AdvanceGenerateDataKeyResponse, _err error) {
	runtime := &dedicatedkmsopenapiutil.RuntimeOptions{}
	_result = &AdvanceGenerateDataKeyResponse{}
	_body, _err := client.AdvanceGenerateDataKeyWithOptions(request, runtime)
	if _err != nil {
		return _result, _err
	}
	_result = _body
	return _result, _err
}

/**
 * 带运行参数调用AdvanceGenerateDataKey接口高级生成数据密钥
 * @param request
 * @param runtime
 * @return AdvanceGenerateDataKeyRequest
 */
func (client *Client) AdvanceGenerateDataKeyWithOptions(request *AdvanceGenerateDataKeyRequest, runtime *dedicatedkmsopenapiutil.RuntimeOptions) (_result *AdvanceGenerateDataKeyResponse, _err error) {
	_err = util.ValidateModel(request)
	if _err != nil {
		return _result, _err
	}
	reqBody := dedicatedkmsopenapiutil.ConvertToMap(request)
	reqBodyBytes, _err := dedicatedkmsopenapiutil.GetSerializedAdvanceGenerateDataKeyRequest(reqBody)
	if _err != nil {
		return _result, _err
	}

	doRequestTmp, err := client.DoRequest(tea.String("AdvanceGenerateDataKey"), tea.String("dkms-gcs-0.2"), tea.String("https"), tea.String("POST"), tea.String("RSA_PKCS1_SHA_256"), reqBodyBytes, runtime, request.Headers)
	if err != nil {
		_err = err
		return _result, _err
	}
	responseEntity, _err := util.AssertAsMap(doRequestTmp)
	if _err != nil {
		return _result, _err
	}

	base64DecodeTmp, err := util.AssertAsString(responseEntity["bodyBytes"])
	if err != nil {
		_err = err
		return _result, _err
	}
	bodyBytes := encodeutil.Base64Decode(base64DecodeTmp)
	respMap, _err := dedicatedkmsopenapiutil.ParseAdvanceGenerateDataKeyResponse(bodyBytes)
	if _err != nil {
		return _result, _err
	}

	_result = &AdvanceGenerateDataKeyResponse{}
	_err = tea.Convert(map[string]interface{}{
		"KeyId":          respMap["KeyId"],
		"Iv":             respMap["Iv"],
		"Plaintext":      respMap["Plaintext"],
		"CiphertextBlob": respMap["CiphertextBlob"],
		"RequestId":      respMap["RequestId"],
		"Algorithm":      respMap["Algorithm"],
		"KeyVersionId":   respMap["KeyVersionId"],
		"headers":        responseEntity["responseHeaders"],
	}, &_result)
	return _result, _err
}
