// This file is auto-generated, don't edit it. Thanks.
package client

import (
  array "github.com/alibabacloud-go/darabonba-array/client"
  map_ "github.com/alibabacloud-go/darabonba-map/client"
  string_ "github.com/alibabacloud-go/darabonba-string/client"
  openapiutil "github.com/alibabacloud-go/openapi-util/service"
  util "github.com/alibabacloud-go/tea-utils/v2/service"
  "github.com/alibabacloud-go/tea/tea"
  dedicatedkmsopenapicredential "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-credential"
  dedicatedkmsopenapiutil "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util"
)

type Config struct {
  // 访问凭证ID
  AccessKeyId *string `json:"accessKeyId,omitempty" xml:"accessKeyId,omitempty"`
  // pkcs1 或 pkcs8 PEM 格式私钥
  PrivateKey *string `json:"privateKey,omitempty" xml:"privateKey,omitempty"`
  // 实例地址
  Endpoint *string `json:"endpoint,omitempty" xml:"endpoint,omitempty"`
  // 协议
  Protocol *string `json:"protocol,omitempty" xml:"protocol,omitempty"`
  // 区域标识
  RegionId *string `json:"regionId,omitempty" xml:"regionId,omitempty" pattern:"[a-zA-Z0-9-_]+"`
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
  // 访问凭证类型
  Type *string `json:"type,omitempty" xml:"type,omitempty" require:"true"`
  // 用户代理
  UserAgent *string `json:"userAgent,omitempty" xml:"userAgent,omitempty"`
  // 访问凭证
  Credential *dedicatedkmsopenapicredential.Client `json:"credential,omitempty" xml:"credential,omitempty"`
  // ClientKey文件路径
  ClientKeyFile *string `json:"clientKeyFile,omitempty" xml:"clientKeyFile,omitempty"`
  // ClientKey文件内容
  ClientKeyContent *string `json:"clientKeyContent,omitempty" xml:"clientKeyContent,omitempty"`
  // ClientKey密码
  Password *string `json:"password,omitempty" xml:"password,omitempty"`
  // ca证书内容
  Ca *string `json:"ca,omitempty" xml:"ca,omitempty"`
  // ca证书文件路径
  CaFilePath *string `json:"caFilePath,omitempty" xml:"caFilePath,omitempty"`
  // 是否忽略SSL认证
  IgnoreSSL *bool `json:"ignoreSSL,omitempty" xml:"ignoreSSL,omitempty"`
}

func (s Config) String() string {
  return tea.Prettify(s)
}

func (s Config) GoString() string {
  return s.String()
}

func (s *Config) SetAccessKeyId(v string) *Config {
  s.AccessKeyId = &v
  return s
}

func (s *Config) SetPrivateKey(v string) *Config {
  s.PrivateKey = &v
  return s
}

func (s *Config) SetEndpoint(v string) *Config {
  s.Endpoint = &v
  return s
}

func (s *Config) SetProtocol(v string) *Config {
  s.Protocol = &v
  return s
}

func (s *Config) SetRegionId(v string) *Config {
  s.RegionId = &v
  return s
}

func (s *Config) SetReadTimeout(v int) *Config {
  s.ReadTimeout = &v
  return s
}

func (s *Config) SetConnectTimeout(v int) *Config {
  s.ConnectTimeout = &v
  return s
}

func (s *Config) SetHttpProxy(v string) *Config {
  s.HttpProxy = &v
  return s
}

func (s *Config) SetHttpsProxy(v string) *Config {
  s.HttpsProxy = &v
  return s
}

func (s *Config) SetNoProxy(v string) *Config {
  s.NoProxy = &v
  return s
}

func (s *Config) SetMaxIdleConns(v int) *Config {
  s.MaxIdleConns = &v
  return s
}

func (s *Config) SetSocks5Proxy(v string) *Config {
  s.Socks5Proxy = &v
  return s
}

func (s *Config) SetSocks5NetWork(v string) *Config {
  s.Socks5NetWork = &v
  return s
}

func (s *Config) SetType(v string) *Config {
  s.Type = &v
  return s
}

func (s *Config) SetUserAgent(v string) *Config {
  s.UserAgent = &v
  return s
}

func (s *Config) SetCredential(v *dedicatedkmsopenapicredential.Client) *Config {
  s.Credential = v
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

func (s *Config) SetCa(v string) *Config {
  s.Ca = &v
  return s
}

func (s *Config) SetCaFilePath(v string) *Config {
  s.CaFilePath = &v
  return s
}

func (s *Config) SetIgnoreSSL(v bool) *Config {
  s.IgnoreSSL = &v
  return s
}

type Client struct {
  Endpoint  *string
  RegionId  *string
  Protocol  *string
  ReadTimeout  *int
  ConnectTimeout  *int
  HttpProxy  *string
  HttpsProxy  *string
  NoProxy  *string
  UserAgent  *string
  Socks5Proxy  *string
  Socks5NetWork  *string
  MaxIdleConns  *int
  Credential  *dedicatedkmsopenapicredential.Client
  Ca  *string
  IgnoreSSL  *bool
}

func NewClient(config *Config)(*Client, error) {
  client := new(Client)
  err := client.Init(config)
  return client, err
}

func (client *Client)Init(config *Config)(_err error) {
  if tea.BoolValue(util.IsUnset(config)) {
    _err = tea.NewSDKError(map[string]interface{}{
      "name": "ParameterMissing",
      "message": "'config' can not be unset",
    })
    return _err
  }

  if tea.BoolValue(util.Empty(config.Endpoint)) {
    _err = tea.NewSDKError(map[string]interface{}{
      "code": "ParameterMissing",
      "message": "'config.endpoint' can not be empty",
    })
    return _err
  } else {
    if tea.BoolValue(string_.HasPrefix(config.Endpoint, tea.String("https://"))) {
      config.Endpoint = string_.Replace(config.Endpoint, tea.String("https://"), tea.String(""), tea.Int(1))
    }

  }

  if !tea.BoolValue(util.Empty(config.ClientKeyContent)) {
    config.Type = tea.String("rsa_key_pair")
    contentConfig := &dedicatedkmsopenapicredential.Config{
      Type: config.Type,
      ClientKeyContent: config.ClientKeyContent,
      Password: config.Password,
    }
    client.Credential, _err = dedicatedkmsopenapicredential.NewClient(contentConfig)
    if _err != nil {
      return _err
    }

  } else if !tea.BoolValue(util.Empty(config.ClientKeyFile)) {
    config.Type = tea.String("rsa_key_pair")
    clientKeyConfig := &dedicatedkmsopenapicredential.Config{
      Type: config.Type,
      ClientKeyFile: config.ClientKeyFile,
      Password: config.Password,
    }
    client.Credential, _err = dedicatedkmsopenapicredential.NewClient(clientKeyConfig)
    if _err != nil {
      return _err
    }

  } else if !tea.BoolValue(util.Empty(config.AccessKeyId)) && !tea.BoolValue(util.Empty(config.PrivateKey)) {
    config.Type = tea.String("rsa_key_pair")
    credentialConfig := &dedicatedkmsopenapicredential.Config{
      Type: config.Type,
      AccessKeyId: config.AccessKeyId,
      PrivateKey: config.PrivateKey,
    }
    client.Credential, _err = dedicatedkmsopenapicredential.NewClient(credentialConfig)
    if _err != nil {
      return _err
    }

  } else if !tea.BoolValue(util.IsUnset(config.Credential)) {
    client.Credential = config.Credential
  }

  if !tea.BoolValue(util.IsUnset(config.Ca)) {
    client.Ca = config.Ca
  } else {
    if !tea.BoolValue(util.IsUnset(config.CaFilePath)) {
      client.Ca, _err = dedicatedkmsopenapiutil.GetCaCertFromFile(config.CaFilePath)
      if _err != nil {
        return _err
      }

    }

  }

  client.Endpoint = config.Endpoint
  client.Protocol = config.Protocol
  client.RegionId = config.RegionId
  client.UserAgent = config.UserAgent
  client.ReadTimeout = config.ReadTimeout
  client.ConnectTimeout = config.ConnectTimeout
  client.HttpProxy = config.HttpProxy
  client.HttpsProxy = config.HttpsProxy
  client.NoProxy = config.NoProxy
  client.Socks5Proxy = config.Socks5Proxy
  client.Socks5NetWork = config.Socks5NetWork
  client.MaxIdleConns = config.MaxIdleConns
  client.IgnoreSSL = config.IgnoreSSL
  return nil
}


func (client *Client) DoRequest(apiName *string, apiVersion *string, protocol *string, method *string, signatureMethod *string, reqBodyBytes []byte, runtime *dedicatedkmsopenapiutil.RuntimeOptions, requestHeaders map[string]*string) (_result map[string]interface{}, _err error) {
  _err = tea.Validate(runtime)
  if _err != nil {
    return _result, _err
  }
  _runtime := map[string]interface{}{
    "timeouted": "retry",
    "readTimeout": tea.IntValue(util.DefaultNumber(runtime.ReadTimeout, client.ReadTimeout)),
    "connectTimeout": tea.IntValue(util.DefaultNumber(runtime.ConnectTimeout, client.ConnectTimeout)),
    "httpProxy": tea.StringValue(util.DefaultString(runtime.HttpProxy, client.HttpProxy)),
    "httpsProxy": tea.StringValue(util.DefaultString(runtime.HttpsProxy, client.HttpsProxy)),
    "noProxy": tea.StringValue(util.DefaultString(runtime.NoProxy, client.NoProxy)),
    "socks5Proxy": tea.StringValue(util.DefaultString(runtime.Socks5Proxy, client.Socks5Proxy)),
    "socks5NetWork": tea.StringValue(util.DefaultString(runtime.Socks5NetWork, client.Socks5NetWork)),
    "maxIdleConns": tea.IntValue(util.DefaultNumber(runtime.MaxIdleConns, client.MaxIdleConns)),
    "retry": map[string]interface{}{
      "retryable": tea.BoolValue(runtime.Autoretry),
      "maxAttempts": tea.IntValue(util.DefaultNumber(runtime.MaxAttempts, tea.Int(3))),
    },
    "backoff": map[string]interface{}{
      "policy": tea.StringValue(util.DefaultString(runtime.BackoffPolicy, tea.String("no"))),
      "period": tea.IntValue(util.DefaultNumber(runtime.BackoffPeriod, tea.Int(1))),
    },
    "ignoreSSL": tea.BoolValue(dedicatedkmsopenapiutil.DefaultBoolean(client.IgnoreSSL, runtime.IgnoreSSL)),
    "cert": tea.StringValue(client.Credential.GetPrivateKeyCert()),
    "key": tea.StringValue(client.Credential.GetAccessKeySecret()),
    "ca": tea.StringValue(util.DefaultString(client.Ca, runtime.Verify)),
  }

  _resp := make(map[string]interface{})
  for _retryTimes := 0; tea.BoolValue(tea.AllowRetry(_runtime["retry"], tea.Int(_retryTimes))); _retryTimes++ {
    if _retryTimes > 0 {
      _backoffTime := tea.GetBackoffTime(_runtime["backoff"], tea.Int(_retryTimes))
      if tea.IntValue(_backoffTime) > 0 {
        tea.Sleep(_backoffTime)
      }
    }

    _resp, _err = func()(map[string]interface{}, error){
      request_ := tea.NewRequest()
      request_.Protocol = util.DefaultString(client.Protocol, protocol)
      request_.Method = method
      request_.Pathname = tea.String("/")
      request_.Headers = tea.Merge(requestHeaders)
      request_.Headers["accept"] = tea.String("application/x-protobuf")
      request_.Headers["host"] = client.Endpoint
      request_.Headers["date"] = util.GetDateUTCString()
      request_.Headers["user-agent"] = util.GetUserAgent(client.UserAgent)
      request_.Headers["x-kms-apiversion"] = apiVersion
      request_.Headers["x-kms-apiname"] = apiName
      request_.Headers["x-kms-signaturemethod"] = signatureMethod
      request_.Headers["x-kms-acccesskeyid"] = client.Credential.GetAccessKeyId()
      request_.Headers["content-type"] = tea.String("application/x-protobuf")
      request_.Headers["content-length"], _err = dedicatedkmsopenapiutil.GetContentLength(reqBodyBytes)
      if _err != nil {
        return _result, _err
      }

      request_.Headers["content-sha256"] = string_.ToUpper(openapiutil.HexEncode(openapiutil.Hash(reqBodyBytes, tea.String("ACS3-RSA-SHA256"))))
      request_.Body = tea.ToReader(reqBodyBytes)
      strToSign, _err := dedicatedkmsopenapiutil.GetStringToSign(method, request_.Pathname, request_.Headers, request_.Query)
      if _err != nil {
        return _result, _err
      }

      request_.Headers["authorization"], _err = client.Credential.GetSignature(strToSign)
      if _err != nil {
        return _result, _err
      }

      response_, _err := tea.DoRequest(request_, _runtime)
      if _err != nil {
        return _result, _err
      }
      var bodyBytes []byte
      if tea.BoolValue(util.Is4xx(response_.StatusCode)) || tea.BoolValue(util.Is5xx(response_.StatusCode)) {
        bodyBytes, _err = util.ReadAsBytes(response_.Body)
        if _err != nil {
          return _result, _err
        }

        assertAsMapTmp, err := dedicatedkmsopenapiutil.GetErrMessage(bodyBytes)
        if err != nil {
          _err = err
          return _result, _err
        }
        respMap, _err := util.AssertAsMap(assertAsMapTmp)
        if _err != nil {
          return _result, _err
        }

        _err = tea.NewSDKError(map[string]interface{}{
          "code": respMap["Code"],
          "message": respMap["Message"],
          "data": map[string]interface{}{
            "httpCode": tea.IntValue(response_.StatusCode),
            "requestId": respMap["RequestId"],
            "hostId": respMap["HostId"],
          },
        })
        return _result, _err
      }

      bodyBytes, _err = util.ReadAsBytes(response_.Body)
      if _err != nil {
        return _result, _err
      }

      responseHeaders := map[string]interface{}{}
      headers := response_.Headers
      if !tea.BoolValue(util.IsUnset(runtime.Headers)) {
        for _, key := range map_.KeySet(headers) {
          if tea.BoolValue(array.Contains(runtime.Headers, key)) {
            responseHeaders[tea.StringValue(key)] = headers[tea.StringValue(key)]
          }

        }
      }

      _result = make(map[string]interface{})
      _err = tea.Convert(map[string]interface{}{
        "bodyBytes": bodyBytes,
        "responseHeaders": responseHeaders,
      }, &_result)
      return _result, _err
    }()
    if !tea.BoolValue(tea.Retryable(_err)) {
      break
    }
  }

  return _resp, _err
}


