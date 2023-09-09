// This file is auto-generated, don't edit it. Thanks.
package client

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	array "github.com/alibabacloud-go/darabonba-array/client"
	map_ "github.com/alibabacloud-go/darabonba-map/client"
	string_ "github.com/alibabacloud-go/darabonba-string/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util/protobuf/api"
	"github.com/golang/protobuf/proto"
	"golang.org/x/crypto/pkcs12"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
)

type RuntimeOptions struct {
  // 是否自动重试
  Autoretry *bool `json:"autoretry,omitempty" xml:"autoretry,omitempty"`
  // 是否忽略SSL认证
  IgnoreSSL *bool `json:"ignoreSSL,omitempty" xml:"ignoreSSL,omitempty"`
  // 最大重试次数
  MaxAttempts *int `json:"maxAttempts,omitempty" xml:"maxAttempts,omitempty"`
  // 回退策略
  BackoffPolicy *string `json:"backoffPolicy,omitempty" xml:"backoffPolicy,omitempty"`
  // 回退周期
  BackoffPeriod *int `json:"backoffPeriod,omitempty" xml:"backoffPeriod,omitempty"`
  // 读取超时时间
  ReadTimeout *int `json:"readTimeout,omitempty" xml:"readTimeout,omitempty"`
  // 连接超时时间
  ConnectTimeout *int `json:"connectTimeout,omitempty" xml:"connectTimeout,omitempty"`
  // http代理
  HttpProxy *string `json:"httpProxy,omitempty" xml:"httpProxy,omitempty"`
  // https代理
  HttpsProxy *string `json:"httpsProxy,omitempty" xml:"httpsProxy,omitempty"`
  // 无代理
  NoProxy *string `json:"noProxy,omitempty" xml:"noProxy,omitempty"`
  // 最大闲置连接数
  MaxIdleConns *int `json:"maxIdleConns,omitempty" xml:"maxIdleConns,omitempty"`
  // socks5代理
  Socks5Proxy *string `json:"socks5Proxy,omitempty" xml:"socks5Proxy,omitempty"`
  // socks5代理协议
  Socks5NetWork *string `json:"socks5NetWork,omitempty" xml:"socks5NetWork,omitempty"`
  // 校验
  Verify *string `json:"verify,omitempty" xml:"verify,omitempty"`
  // 响应头
  Headers []*string `json:"headers,omitempty" xml:"headers,omitempty" type:"Repeated"`
}

func (s RuntimeOptions) String() string {
  return tea.Prettify(s)
}

func (s RuntimeOptions) GoString() string {
  return s.String()
}

func (s *RuntimeOptions) SetAutoretry(v bool) *RuntimeOptions {
  s.Autoretry = &v
  return s
}

func (s *RuntimeOptions) SetIgnoreSSL(v bool) *RuntimeOptions {
  s.IgnoreSSL = &v
  return s
}

func (s *RuntimeOptions) SetMaxAttempts(v int) *RuntimeOptions {
  s.MaxAttempts = &v
  return s
}

func (s *RuntimeOptions) SetBackoffPolicy(v string) *RuntimeOptions {
  s.BackoffPolicy = &v
  return s
}

func (s *RuntimeOptions) SetBackoffPeriod(v int) *RuntimeOptions {
  s.BackoffPeriod = &v
  return s
}

func (s *RuntimeOptions) SetReadTimeout(v int) *RuntimeOptions {
  s.ReadTimeout = &v
  return s
}

func (s *RuntimeOptions) SetConnectTimeout(v int) *RuntimeOptions {
  s.ConnectTimeout = &v
  return s
}

func (s *RuntimeOptions) SetHttpProxy(v string) *RuntimeOptions {
  s.HttpProxy = &v
  return s
}

func (s *RuntimeOptions) SetHttpsProxy(v string) *RuntimeOptions {
  s.HttpsProxy = &v
  return s
}

func (s *RuntimeOptions) SetNoProxy(v string) *RuntimeOptions {
  s.NoProxy = &v
  return s
}

func (s *RuntimeOptions) SetMaxIdleConns(v int) *RuntimeOptions {
  s.MaxIdleConns = &v
  return s
}

func (s *RuntimeOptions) SetSocks5Proxy(v string) *RuntimeOptions {
  s.Socks5Proxy = &v
  return s
}

func (s *RuntimeOptions) SetSocks5NetWork(v string) *RuntimeOptions {
  s.Socks5NetWork = &v
  return s
}

func (s *RuntimeOptions) SetVerify(v string) *RuntimeOptions {
  s.Verify = &v
  return s
}

func (s *RuntimeOptions) SetHeaders(v []*string) *RuntimeOptions {
  s.Headers = v
  return s
}

type ErrorResponse struct {
  // 
  StatusCode *string `json:"StatusCode,omitempty" xml:"StatusCode,omitempty" require:"true"`
  // 
  ErrorCode *string `json:"ErrorCode,omitempty" xml:"ErrorCode,omitempty" require:"true"`
  // 
  ErrorMessage *string `json:"ErrorMessage,omitempty" xml:"ErrorMessage,omitempty" require:"true"`
  // 
  RequestId *string `json:"RequestId,omitempty" xml:"RequestId,omitempty" require:"true"`
}

func (s ErrorResponse) String() string {
  return tea.Prettify(s)
}

func (s ErrorResponse) GoString() string {
  return s.String()
}

func (s *ErrorResponse) SetStatusCode(v string) *ErrorResponse {
  s.StatusCode = &v
  return s
}

func (s *ErrorResponse) SetErrorCode(v string) *ErrorResponse {
  s.ErrorCode = &v
  return s
}

func (s *ErrorResponse) SetErrorMessage(v string) *ErrorResponse {
  s.ErrorMessage = &v
  return s
}

func (s *ErrorResponse) SetRequestId(v string) *ErrorResponse {
  s.RequestId = &v
  return s
}


func GetErrMessage (msg []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.Error{}
	_err = proto.Unmarshal(msg, response)
	if _err != nil {
		_err = errors.New(fmt.Sprintf("proto.Unmarshal(%s), err:%v", string(msg), _err))
		return
	}
	_result["Code"] = response.ErrorCode
	_result["Message"] = response.ErrorMessage
	_result["RequestId"] = response.RequestId
	return
}

func GetContentLength (reqBody []byte) (_result *string, _err error) {

	 return tea.String(strconv.Itoa(len(reqBody))),nil
}

func GetCaCertFromFile (reqBody *string) (_result *string, _err error) {

	file, _err := os.Open(tea.StringValue(reqBody))
	if _err != nil {
		return nil, _err
	}
	defer file.Close()
	return util.ReadAsString(file)
}

func GetPrivatePemFromPk12 (privateKeyData []byte, password *string) (_result []*string, _err error) {

	blocks, err := pkcs12.ToPEM(privateKeyData, tea.StringValue(password))
	if err != nil {
		return nil, err
	}
	return []*string{tea.String(string(pem.EncodeToMemory(blocks[1]))) , tea.String(string(pem.EncodeToMemory(blocks[0])))}, nil
}

func GetStringToSign (method *string, pathname *string, headers map[string]*string, query map[string]*string) (_result *string, _err error) {
  contentSHA256 := headers["content-sha256"]
  if tea.BoolValue(util.IsUnset(contentSHA256)) {
    contentSHA256 = tea.String("")
  }

  contentType := headers["content-type"]
  if tea.BoolValue(util.IsUnset(contentType)) {
    contentType = tea.String("")
  }

  date := headers["date"]
  if tea.BoolValue(util.IsUnset(date)) {
    date = tea.String("")
  }

  header := tea.String(tea.StringValue(method) + "\n" + tea.StringValue(contentSHA256) + "\n" + tea.StringValue(contentType) + "\n" + tea.StringValue(date) + "\n")
  canonicalizedHeaders, _err := GetCanonicalizedHeaders(headers)
  if _err != nil {
    return _result, _err
  }

  canonicalizedResource, _err := GetCanonicalizedResource(pathname, query)
  if _err != nil {
    return _result, _err
  }

  _result = tea.String(tea.StringValue(header) + tea.StringValue(canonicalizedHeaders) + tea.StringValue(canonicalizedResource))
  return _result, _err
}

func ReadJsonFile (jsonFile *string) (_result map[string]interface{}, _err error) {

	file, err := os.Open(tea.StringValue(jsonFile))
	if err != nil {
		return nil, nil
	}
	defer file.Close()
	json, _err := util.ReadAsJSON(file)
	if _err != nil {
		return nil, _err
	}
	return json.(map[string]interface {}),_err
}

func GetCanonicalizedHeaders (headers map[string]*string) (_result *string, _err error) {
  if tea.BoolValue(util.IsUnset(headers)) {
    _result = nil
    return _result , _err
  }

  prefix := tea.String("x-kms-")
  keys := map_.KeySet(headers)
  sortedKeys := array.AscSort(keys)
  canonicalizedHeaders := tea.String("")
  for _, key := range sortedKeys {
    if tea.BoolValue(string_.HasPrefix(key, prefix)) {
      canonicalizedHeaders = tea.String(tea.StringValue(canonicalizedHeaders) + tea.StringValue(key) + ":" + tea.StringValue(string_.Trim(headers[tea.StringValue(key)])) + "\n")
    }

  }
  _result = canonicalizedHeaders
  return _result , _err
}

func GetCanonicalizedResource (pathname *string, query map[string]*string) (_result *string, _err error) {
  if !tea.BoolValue(util.IsUnset(pathname)) {
    _result = tea.String("/")
    return _result, _err
  }

  if tea.BoolValue(util.IsUnset(query)) {
    _result = pathname
    return _result , _err
  }

  canonicalizedResource := tea.String("")
  queryArray := map_.KeySet(query)
  sortedQueryArray := array.AscSort(queryArray)
  separator := tea.String("")
  canonicalizedResource = tea.String(tea.StringValue(pathname) + "?")
  for _, key := range sortedQueryArray {
    canonicalizedResource = tea.String(tea.StringValue(canonicalizedResource) + tea.StringValue(separator) + tea.StringValue(key))
    if !tea.BoolValue(util.Empty(query[tea.StringValue(key)])) {
      canonicalizedResource = tea.String(tea.StringValue(canonicalizedResource) + "=" + tea.StringValue(query[tea.StringValue(key)]))
    }

    separator = tea.String("&")
  }
  _result = canonicalizedResource
  return _result , _err
}

func DefaultBoolean (bool1 *bool, bool2 *bool) (_result *bool) {
  if tea.BoolValue(util.IsUnset(bool1)) {
    _result = bool2
    return _result
  } else {
    _result = bool1
    return _result
  }

}

func SignString (stringToSign *string, accessKeySecret *string) (_result *string, _err error) {

	block, _ := pem.Decode([]byte(tea.StringValue(accessKeySecret)))
	pkcs1Priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	hashed := sha256.Sum256([]byte(tea.StringValue(stringToSign)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, pkcs1Priv, crypto.SHA256, hashed[:])
	if err != nil {
		return nil, err
	}
	return tea.String(fmt.Sprintf("Bearer %s", base64.StdEncoding.EncodeToString(sig))), nil
}

func ConvertToMap (body interface{}) (_result map[string]interface{}) {

	res := make(map[string]interface{})
	val := reflect.ValueOf(body).Elem()
	dataType := val.Type()
	for i := 0; i < dataType.NumField(); i++ {
		field := dataType.Field(i)
		name, _ := field.Tag.Lookup("json")
		name = strings.Split(name, ",omitempty")[0]
		_, ok := val.Field(i).Interface().(io.Reader)
		if !ok {
			res[name] = val.Field(i).Interface()
		}
	}
	return res
}

func GetSerializedEncryptRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.EncryptRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Plaintext"]; ok {
		request.Plaintext = v.([]byte)
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	if v, ok := reqBody["Iv"]; ok {
		request.Iv = v.([]byte)
	}
	if v, ok := reqBody["PaddingMode"]; ok {
		request.PaddingMode = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseEncryptResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.EncryptResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["CiphertextBlob"] = response.CiphertextBlob
	_result["Iv"] = response.Iv
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["PaddingMode"] = tea.String(response.PaddingMode)
	return
}

func GetSerializedDecryptRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.DecryptRequest{}
	if v, ok := reqBody["CiphertextBlob"]; ok {
		request.CiphertextBlob = v.([]byte)
	}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	if v, ok := reqBody["Iv"]; ok {
		request.Iv = v.([]byte)
	}
	if v, ok := reqBody["PaddingMode"]; ok {
		request.PaddingMode = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseDecryptResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.DecryptResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Plaintext"] = response.Plaintext
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["PaddingMode"] = tea.String(response.PaddingMode)
	return
}

func GetSerializedSignRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.SignRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Digest"]; ok {
		request.Digest = v.([]byte)
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Message"]; ok {
		request.Message = v.([]byte)
	}
	if v, ok := reqBody["MessageType"]; ok {
		request.MessageType = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseSignResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.SignResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Signature"] = response.Signature
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["MessageType"] = tea.String(response.MessageType)
	return
}

func GetSerializedVerifyRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.VerifyRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Digest"]; ok {
		request.Digest = v.([]byte)
	}
	if v, ok := reqBody["Signature"]; ok {
		request.Signature = v.([]byte)
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Message"]; ok {
		request.Message = v.([]byte)
	}
	if v, ok := reqBody["MessageType"]; ok {
		request.MessageType = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseVerifyResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.VerifyResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Value"] = tea.Bool(response.Value)
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["MessageType"] = tea.String(response.MessageType)
	return
}

func GetSerializedGenerateDataKeyRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.GenerateDataKeyRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["NumberOfBytes"]; ok {
		request.NumberOfBytes = tea.Int32Value(v.(*int32))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseGenerateDataKeyResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.GenerateDataKeyResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Iv"] = response.Iv
	_result["Plaintext"] = response.Plaintext
	_result["CiphertextBlob"] = response.CiphertextBlob
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	return
}

func GetSerializedGetPublicKeyRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.GetPublicKeyRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseGetPublicKeyResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.GetPublicKeyResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["PublicKey"] = tea.String(response.PublicKey)
	_result["RequestId"] = tea.String(response.RequestId)
	return
}

func GetSerializedGetSecretValueRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.GetSecretValueRequest{}
	if v, ok := reqBody["SecretName"]; ok {
		request.SecretName = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["VersionStage"]; ok {
		request.VersionStage = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["VersionId"]; ok {
		request.VersionId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["FetchExtendedConfig"]; ok {
		request.FetchExtendedConfig = tea.BoolValue(v.(*bool))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseGetSecretValueResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.GetSecretValueResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["SecretName"] = tea.String(response.SecretName)
	_result["SecretType"] = tea.String(response.SecretType)
	_result["SecretData"] = tea.String(response.SecretData)
	_result["SecretDataType"] = tea.String(response.SecretDataType)
	var versionStages []*string
	for _, x := range response.VersionStages {
		versionStages = append(versionStages, tea.String(x))
	}
	_result["VersionStages"] = versionStages
	_result["VersionId"] = tea.String(response.VersionId)
	_result["CreateTime"] = tea.String(response.CreateTime)
	_result["RequestId"] = tea.String(response.RequestId)
	_result["LastRotationDate"] = tea.String(response.LastRotationDate)
	_result["NextRotationDate"] = tea.String(response.NextRotationDate)
	_result["ExtendedConfig"] = tea.String(response.ExtendedConfig)
	_result["AutomaticRotation"] = tea.String(response.AutomaticRotation)
	_result["RotationInterval"] = tea.String(response.RotationInterval)
	return
}

func GetSerializedAdvanceEncryptRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.AdvanceEncryptRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Plaintext"]; ok {
		request.Plaintext = v.([]byte)
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	if v, ok := reqBody["Iv"]; ok {
		request.Iv = v.([]byte)
	}
	if v, ok := reqBody["PaddingMode"]; ok {
		request.PaddingMode = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseAdvanceEncryptResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.AdvanceEncryptResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["CiphertextBlob"] = response.CiphertextBlob
	_result["Iv"] = response.Iv
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["PaddingMode"] = tea.String(response.PaddingMode)
	_result["KeyVersionId"] = tea.String(response.KeyVersionId)
	return
}

func GetSerializedAdvanceDecryptRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.AdvanceDecryptRequest{}
	if v, ok := reqBody["CiphertextBlob"]; ok {
		request.CiphertextBlob = v.([]byte)
	}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Algorithm"]; ok {
		request.Algorithm = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	if v, ok := reqBody["Iv"]; ok {
		request.Iv = v.([]byte)
	}
	if v, ok := reqBody["PaddingMode"]; ok {
		request.PaddingMode = tea.StringValue(v.(*string))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseAdvanceDecryptResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.AdvanceDecryptResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Plaintext"] = response.Plaintext
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["PaddingMode"] = tea.String(response.PaddingMode)
	_result["KeyVersionId"] = tea.String(response.KeyVersionId)
	return
}

func GetSerializedAdvanceGenerateDataKeyRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.AdvanceGenerateDataKeyRequest{}
	if v, ok := reqBody["KeyId"]; ok {
		request.KeyId = tea.StringValue(v.(*string))
	}
	if v, ok := reqBody["NumberOfBytes"]; ok {
		request.NumberOfBytes = tea.Int32Value(v.(*int32))
	}
	if v, ok := reqBody["Aad"]; ok {
		request.Aad = v.([]byte)
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseAdvanceGenerateDataKeyResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.AdvanceGenerateDataKeyResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["KeyId"] = tea.String(response.KeyId)
	_result["Iv"] = response.Iv
	_result["Plaintext"] = response.Plaintext
	_result["CiphertextBlob"] = response.CiphertextBlob
	_result["RequestId"] = tea.String(response.RequestId)
	_result["Algorithm"] = tea.String(response.Algorithm)
	_result["KeyVersionId"] = tea.String(response.KeyVersionId)
	return
}

func GetSerializedGenerateRandomRequest (reqBody map[string]interface{}) (_result []byte, _err error) {

	request := &api.GenerateRandomRequest{}
	if v, ok := reqBody["Length"]; ok {
		request.Length = tea.Int32Value(v.(*int32))
	}
	_result, _err = proto.Marshal(request)
	return
}

func ParseGenerateRandomResponse (resBody []byte) (_result map[string]interface{}, _err error) {

	_result = make(map[string]interface{})
	response := &api.GenerateRandomResponse{}
	_err = proto.Unmarshal(resBody, response)
	if _err != nil {
		return
	}
	_result["Random"] = response.Random
	_result["RequestId"] = tea.String(response.RequestId)
	return
}

