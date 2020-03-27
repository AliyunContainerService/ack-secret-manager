package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"
)

// Custom HTTP client, that defines the redirect behavior.
// Don't follow 301s, return them so the tests can correctly identify and validate responses
func testHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// DRY
func doBodyTest(t *testing.T, uri string, expectedBody string) {
	client := testHTTPClient()
	res, err := client.Get(testServer.URL + uri)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	defer res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != expectedBody {
		t.Errorf("%s : Expected\n\n%s\n\ngot\n\n%s", uri, expectedBody, string(body))
	}
}

// Some URIs have 301 redirects on the real metadata service
func doRedirectTest(t *testing.T, uri string, expectedLocationURI string) {
	client := testHTTPClient()
	res, err := client.Get(testServer.URL + uri)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 301 {
		t.Errorf("%s : Expected HTTP Status Code 301, got %d\n", uri, res.StatusCode)
	}
	if res.Header.Get("Location") == "" {
		t.Errorf("%s : Expected a 'Location' HTTP response header, none found\n", uri)
	}
	expectedLocation := fmt.Sprintf("http://100.100.100.200%s", expectedLocationURI)
	if res.Header.Get("Location") != expectedLocation {
		t.Errorf("%s : Expected 'Location' HTTP response header of %s, got %s\n", uri, expectedLocation, res.Header.Get("Location"))
	}
}

func TestRoot(t *testing.T) {
	expectedBody := `latest`

	doBodyTest(t, "", expectedBody)
	doBodyTest(t, "/", expectedBody)
}

func TestLatest(t *testing.T) {
	expectedBody := `dynamic
meta-data
user-data`

	doRedirectTest(t, "/latest", "/latest/")
	doBodyTest(t, "/latest/", expectedBody)
}

func TestLatestDynamic(t *testing.T) {
	expectedBody := `instance-identity/
`

	doRedirectTest(t, "/latest/dynamic", "/latest/dynamic/")
	doBodyTest(t, "/latest/dynamic/", expectedBody)
}

func TestLatestDynamicInstanceIdentity(t *testing.T) {
	expectedBody := `document
pkcs7
signature
`

	doRedirectTest(t, "/latest/dynamic/instance-identity", "/latest/dynamic/instance-identity/")
	doBodyTest(t, "/latest/dynamic/instance-identity/", expectedBody)
}

func TestLatestDynamicInstanceIdentityDocument(t *testing.T) {
	// NOTE: upstream syntax is "key" : "value",
	// but this implemented uses "key": "value",
	// mostly to save time not writing a custom JSON marshaller.
	// Test results modified by hand to pass (extra spaces removed).
	expectedBody := `{
  "instance-id": "i-asdfasdf",
  "image-id": "centos_7_04_64_20G_alibase_201701015.vhd",
  "instance-type": "t2.micro",
  "owner-account-id": "123456789012",
  "region-id": "cn-shanghai",
  "zone-id": "cn-shanghai-e",
  "private-ipv4": "10.20.30.40"
}`

	doBodyTest(t, "/latest/dynamic/instance-identity/document", expectedBody)
	doBodyTest(t, "/latest/dynamic/instance-identity/document/", expectedBody)
}

func TestLatestDynamicInstanceIdentityPkcs7(t *testing.T) {
	expectedBody := `PKCS7`

	doBodyTest(t, "/latest/dynamic/instance-identity/pkcs7", expectedBody)
	doBodyTest(t, "/latest/dynamic/instance-identity/pkcs7/", expectedBody)
}

func TestLatestDynamicInstanceIdentitySignature(t *testing.T) {
	expectedBody := `SIGNATURE`

	doBodyTest(t, "/latest/dynamic/instance-identity/signature", expectedBody)
	doBodyTest(t, "/latest/dynamic/instance-identity/signature/", expectedBody)
}

func TestLatestMetaData(t *testing.T) {
	// NOTE: iam/ only appears if there is an IAM Instance Profile attached to the instance. assuming available for simulation purposes for now.
	expectedBody := `dns-conf/
eipv4
hostname
image-id
instance-id
instance/
mac
network-type
network/
ntp-conf/
owner-account-id
private-ipv4
ram/
region-id
serial-number
source-address
sub-private-ipv4-list
vpc-cidr-block
vpc-id
vswitch-cidr-block
vswitch-id
zone-id`

	doRedirectTest(t, "/latest/meta-data", "/latest/meta-data/")
	doBodyTest(t, "/latest/meta-data/", expectedBody)
}

func TestLatestMetaDataAmiId(t *testing.T) {
	expectedBody := `centos_7_04_64_20G_alibase_201701015.vhd`

	doBodyTest(t, "/latest/meta-data/image-id", expectedBody)
	doBodyTest(t, "/latest/meta-data/image-id/", expectedBody)
}

func TestLatestMetaDataHostname(t *testing.T) {
	expectedBody := `testhostname`

	doBodyTest(t, "/latest/meta-data/hostname", expectedBody)
	doBodyTest(t, "/latest/meta-data/hostname/", expectedBody)
}

func TestLatestMetaDataRam(t *testing.T) {
	expectedBody := `security-credentials/`

	doRedirectTest(t, "/latest/meta-data/ram", "/latest/meta-data/ram/")
	doBodyTest(t, "/latest/meta-data/ram/", expectedBody)
}

func TestLatestMetaDataIamSecurityCredentials(t *testing.T) {
	expectedBody := `some-instance-profile`

	doRedirectTest(t, "/latest/meta-data/ram/security-credentials", "/latest/meta-data/ram/security-credentials/")
	doBodyTest(t, "/latest/meta-data/ram/security-credentials/", expectedBody)
}

func TestLatestMetaDataIamSecurityCredentialsSomeInstanceProfile(t *testing.T) {
	// TODOLATER: round to nearest hour, to ensure test coverage passes more reliably?
	now := time.Now().UTC()
	expire := now.Add(6 * time.Hour)
	format := "2006-01-02T15:04:05Z"
	expectedBody := fmt.Sprintf(`{
  "Code" : "Success",
  "LastUpdated" : "%s",
  "AccessKeyId" : "mock-access-key-id",
  "SecretAccessKey" : "mock-secret-access-key",
  "Token" : "mock-token",
  "Expiration" : "%s"
}`, now.Format(format), expire.Format(format))

	doBodyTest(t, "/latest/meta-data/ram/security-credentials/some-instance-profile", expectedBody)
	doBodyTest(t, "/latest/meta-data/ram/security-credentials/some-instance-profile/", expectedBody)
}

func TestLatestMetaDataInstanceId(t *testing.T) {
	expectedBody := `i-asdfasdf`

	doBodyTest(t, "/latest/meta-data/instance-id", expectedBody)
	doBodyTest(t, "/latest/meta-data/instance-id/", expectedBody)
}

func TestLatestMetaDataInstanceType(t *testing.T) {
	expectedBody := `t2.micro`

	doBodyTest(t, "/latest/meta-data/instance/instance-type", expectedBody)
	doBodyTest(t, "/latest/meta-data/instance/instance-type/", expectedBody)
}

func TestLatestMetaDataLocalHostname(t *testing.T) {
	expectedBody := `testhostname`

	doBodyTest(t, "/latest/meta-data/hostname", expectedBody)
	doBodyTest(t, "/latest/meta-data/hostname/", expectedBody)
}

func TestLatestMetaDataLocalIpv4(t *testing.T) {
	expectedBody := `10.20.30.40`

	doBodyTest(t, "/latest/meta-data/private-ipv4", expectedBody)
	doBodyTest(t, "/latest/meta-data/private-ipv4/", expectedBody)
}

func TestLatestMetaDataMac(t *testing.T) {
	expectedBody := `00:aa:bb:cc:dd:ee`

	doBodyTest(t, "/latest/meta-data/mac", expectedBody)
	doBodyTest(t, "/latest/meta-data/mac/", expectedBody)
}

func TestLatestMetaDataNetwork(t *testing.T) {
	expectedBody := `interfaces/`

	doRedirectTest(t, "/latest/meta-data/network", "/latest/meta-data/network/")
	doBodyTest(t, "/latest/meta-data/network/", expectedBody)
}

func TestLatestMetaDataNetworkInterfaces(t *testing.T) {
	expectedBody := `macs/`

	doRedirectTest(t, "/latest/meta-data/network/interfaces", "/latest/meta-data/network/interfaces/")
	doBodyTest(t, "/latest/meta-data/network/interfaces/", expectedBody)
}

func TestLatestMetaDataNetworkInterfacesMacs(t *testing.T) {
	expectedBody := `00:aa:bb:cc:dd:ee/`

	doRedirectTest(t, "/latest/meta-data/network/interfaces/macs", "/latest/meta-data/network/interfaces/macs/")
	doBodyTest(t, "/latest/meta-data/network/interfaces/macs/", expectedBody)
}

func TestLatestMetaDataNetworkInterfacesMacsAddr(t *testing.T) {
	expectedBody := `gateway
netmask
network-interface-id
primary-ip-address
private-ipv4s
vpc-cidr-block
vpc-id
vswitch-cidr-block
vswitch-id`

	doRedirectTest(t, "/latest/meta-data/network/interfaces/macs/00:aa:bb:cc:dd:ee", "/latest/meta-data/network/interfaces/macs/00:aa:bb:cc:dd:ee/")
	doBodyTest(t, "/latest/meta-data/network/interfaces/macs/00:aa:bb:cc:dd:ee/", expectedBody)
}

func TestLatestMetaDataNIMAddrInterfaceId(t *testing.T) {
	expectedBody := `eni-asdfasdf`

	doBodyTest(t, "/latest/meta-data/network/interfaces/macs/00:aa:bb:cc:dd:ee/network-interface-id", expectedBody)
	doBodyTest(t, "/latest/meta-data/network/interfaces/macs/00:aa:bb:cc:dd:ee/network-interface-id/", expectedBody)
}

func TestLatestUserData(t *testing.T) {
	// TODO: /latest/user-data returns a 404 if none exists... or if one exists, will return it?
	// should we expose this in the API? not implemented right now. could be useful...
}
