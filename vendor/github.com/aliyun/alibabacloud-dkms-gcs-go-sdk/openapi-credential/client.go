// This file is auto-generated, don't edit it. Thanks.
package client

import (
  encodeutil "github.com/alibabacloud-go/darabonba-encode-util/client"
  util "github.com/alibabacloud-go/tea-utils/v2/service"
  "github.com/alibabacloud-go/tea/tea"
  dedicatedkmsopenapiutil "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util"
)

type Config struct {
  // 访问凭证类型
  Type *string `json:"Type,omitempty" xml:"Type,omitempty" require:"true"`
  // 访问凭证ID
  AccessKeyId *string `json:"Type,omitempty" xml:"Type,omitempty"`
  // pkcs1 或 pkcs8 PEM 格式私钥
  PrivateKey *string `json:"Type,omitempty" xml:"Type,omitempty"`
  // ClientKey文件路径
  ClientKeyFile *string `json:"Type,omitempty" xml:"Type,omitempty"`
  // ClientKey文件内容
  ClientKeyContent *string `json:"Type,omitempty" xml:"Type,omitempty"`
  // ClientKey密码
  Password *string `json:"Type,omitempty" xml:"Type,omitempty"`
}

func (s Config) String() string {
  return tea.Prettify(s)
}

func (s Config) GoString() string {
  return s.String()
}

func (s *Config) SetType(v string) *Config {
  s.Type = &v
  return s
}

func (s *Config) SetAccessKeyId(v string) *Config {
  s.AccessKeyId = &v
  return s
}

func (s *Config) SetPrivateKey(v string) *Config {
  s.PrivateKey = &v
  return s
}

func (s *Config) SetClientKeyFile(v string) *Config {
  s.ClientKeyFile = &v
  return s
}

func (s *Config) SetClientKeyContent(v string) *Config {
  s.ClientKeyContent = &v
  return s
}

func (s *Config) SetPassword(v string) *Config {
  s.Password = &v
  return s
}

type RsaKeyPairCredentials struct {
  // 访问凭证私钥
  PrivateKeySecret *string `json:"privateKeySecret,omitempty" xml:"privateKeySecret,omitempty"`
  // 访问凭证ID
  KeyId *string `json:"keyId,omitempty" xml:"keyId,omitempty"`
}

func (s RsaKeyPairCredentials) String() string {
  return tea.Prettify(s)
}

func (s RsaKeyPairCredentials) GoString() string {
  return s.String()
}

func (s *RsaKeyPairCredentials) SetPrivateKeySecret(v string) *RsaKeyPairCredentials {
  s.PrivateKeySecret = &v
  return s
}

func (s *RsaKeyPairCredentials) SetKeyId(v string) *RsaKeyPairCredentials {
  s.KeyId = &v
  return s
}

type Client struct {
  KeyId  *string
  PrivateKeySecret  *string
  PrivateKeyCert  *string
}

func NewClient(config *Config)(*Client, error) {
  client := new(Client)
  err := client.Init(config)
  return client, err
}

func (client *Client)Init(config *Config)(_err error) {
  if tea.BoolValue(util.EqualString(tea.String("rsa_key_pair"), config.Type)) {
    if !tea.BoolValue(util.Empty(config.ClientKeyContent)) {
      json := util.ParseJSON(config.ClientKeyContent)
      clientKey, _err := util.AssertAsMap(json)
      if _err != nil {
        return  _err
      }

      base64DecodeTmp, err := util.AssertAsString(clientKey["PrivateKeyData"])
      if err != nil {
        _err = err
        return _err
      }
      privateKeyData := encodeutil.Base64Decode(base64DecodeTmp)
      privateKeyFromContent, _err := dedicatedkmsopenapiutil.GetPrivatePemFromPk12(privateKeyData, config.Password)
      if _err != nil {
        return  _err
      }

      client.PrivateKeySecret = privateKeyFromContent[0]
      client.PrivateKeyCert = privateKeyFromContent[1]
      client.KeyId, _err = util.AssertAsString(clientKey["KeyId"])
      if _err != nil {
        return _err
      }

    } else if !tea.BoolValue(util.Empty(config.ClientKeyFile)) {
      jsonFromFile, _err := dedicatedkmsopenapiutil.ReadJsonFile(config.ClientKeyFile)
      if _err != nil {
        return  _err
      }

      if tea.BoolValue(util.IsUnset(jsonFromFile)) {
        _err = tea.NewSDKError(map[string]interface{}{
          "message": "read client key file failed: " + tea.StringValue(config.ClientKeyFile),
        })
        return _err
      }

      clientKeyFromFile, _err := util.AssertAsMap(jsonFromFile)
      if _err != nil {
        return  _err
      }

      base64DecodeTmp, err := util.AssertAsString(clientKeyFromFile["PrivateKeyData"])
      if err != nil {
        _err = err
        return _err
      }
      privateKeyDataFromFile := encodeutil.Base64Decode(base64DecodeTmp)
      privateKeyFromFile, _err := dedicatedkmsopenapiutil.GetPrivatePemFromPk12(privateKeyDataFromFile, config.Password)
      if _err != nil {
        return  _err
      }

      client.PrivateKeySecret = privateKeyFromFile[0]
      client.PrivateKeyCert = privateKeyFromFile[1]
      client.KeyId, _err = util.AssertAsString(clientKeyFromFile["KeyId"])
      if _err != nil {
        return _err
      }

    } else {
      client.PrivateKeySecret = config.PrivateKey
      client.KeyId = config.AccessKeyId
    }

  } else {
    _err = tea.NewSDKError(map[string]interface{}{
      "message": "Only support rsa key pair credential provider now.",
    })
    return _err
  }

  return nil
}



func (client *Client) GetAccessKeyId () (_result *string) {
  _result = client.KeyId
  return _result
}

func (client *Client) GetAccessKeySecret () (_result *string) {
  _result = client.PrivateKeySecret
  return _result
}

func (client *Client) GetPrivateKeyCert () (_result *string) {
  _result = client.PrivateKeyCert
  return _result
}

func (client *Client) GetSignature (strToSign *string) (_result *string, _err error) {

  signature, _err := dedicatedkmsopenapiutil.SignString(strToSign, client.PrivateKeySecret)
  if _err != nil {
    return  signature,_err
  }

  _result = signature
  return _result , _err
}

