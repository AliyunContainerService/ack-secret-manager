package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/sts"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// StartServer starts a newly created http server
func (app *App) StartServer() {
	log.Infof("Listening on port %s", app.AppPort)
	if err := http.ListenAndServe(":"+app.AppPort, app.NewServer()); err != nil {
		log.Fatalf("Error creating http server: %+v", err)
	}
}

func (app *App) apiVersionPrefixes() []string {
	return []string{
		"latest",
	}
}

// NewServer creates a new http server (starting handled separately to allow test suites to reuse)
func (app *App) NewServer() *mux.Router {
	r := mux.NewRouter()
	r.Handle("", appHandler(app.rootHandler))
	r.Handle("/", appHandler(app.rootHandler))

	for _, v := range app.apiVersionPrefixes() {
		d := r.PathPrefix(fmt.Sprintf("/%s", v)).Subrouter()
		app.versionSubRouter(d, v)
	}

	r.Handle("/{path:.*}", appHandler(app.notFoundHandler))

	return r
}

// Provides the versioned (normally 1.0, YYYY-MM-DD or latest) prefix routes
// TODO: conditional out the namespaces that don't exist on selected API versions
func (app *App) versionSubRouter(sr *mux.Router, version string) {
	sr.Handle("", appHandler(app.trailingSlashRedirect))
	sr.Handle("/", appHandler(app.secondLevelHandler))

	d := sr.PathPrefix("/dynamic").Subrouter()
	d.Handle("", appHandler(app.trailingSlashRedirect))
	d.Handle("/", appHandler(app.dynamicHandler))

	ii := d.PathPrefix("/instance-identity").Subrouter()
	ii.Handle("", appHandler(app.trailingSlashRedirect))
	ii.Handle("/", appHandler(app.instanceIdentityHandler))
	ii.Handle("/document", appHandler(app.instanceIdentityDocumentHandler))
	ii.Handle("/document/", appHandler(app.instanceIdentityDocumentHandler))
	ii.Handle("/pkcs7", appHandler(app.instanceIdentityPkcs7Handler))
	ii.Handle("/pkcs7/", appHandler(app.instanceIdentityPkcs7Handler))
	ii.Handle("/signature", appHandler(app.instanceIdentitySignatureHandler))
	ii.Handle("/signature/", appHandler(app.instanceIdentitySignatureHandler))

	m := sr.PathPrefix("/meta-data").Subrouter()
	m.Handle("", appHandler(app.trailingSlashRedirect))
	m.Handle("/", appHandler(app.metaDataHandler))
	m.Handle("/image-id", appHandler(app.imageIDHandler))
	m.Handle("/image-id/", appHandler(app.imageIDHandler))

	m.Handle("/hostname", appHandler(app.hostnameHandler))
	m.Handle("/hostname/", appHandler(app.hostnameHandler))

	r := m.PathPrefix("/ram").Subrouter()
	r.Handle("", appHandler(app.trailingSlashRedirect))
	r.Handle("/", appHandler(app.ramHandler))
	rsc := r.PathPrefix("/security-credentials").Subrouter()
	rsc.Handle("", appHandler(app.trailingSlashRedirect))
	rsc.Handle("/", appHandler(app.securityCredentialsHandler))
	if app.MockInstanceProfile {
		rsc.Handle("/"+app.RoleName, appHandler(app.mockRoleHandler))
		rsc.Handle("/"+app.RoleName+"/", appHandler(app.mockRoleHandler))
	} else {
		rsc.Handle("/"+app.RoleName, appHandler(app.roleHandler))
		rsc.Handle("/"+app.RoleName+"/", appHandler(app.roleHandler))
	}

	i := m.PathPrefix("/instance").Subrouter()
	i.Handle("", appHandler(app.trailingSlashRedirect))
	i.Handle("/instance-type", appHandler(app.instanceTypeHandler))
	i.Handle("/instance-type/", appHandler(app.instanceTypeHandler))

	m.Handle("/instance-id", appHandler(app.instanceIDHandler))
	m.Handle("/instance-id/", appHandler(app.instanceIDHandler))
	m.Handle("/private-ipv4", appHandler(app.privateIPHandler))
	m.Handle("/private-ipv4/", appHandler(app.privateIPHandler))
	m.Handle("/mac", appHandler(app.macHandler))
	m.Handle("/mac/", appHandler(app.macHandler))

	n := m.PathPrefix("/network").Subrouter()
	n.Handle("", appHandler(app.trailingSlashRedirect))
	n.Handle("/", appHandler(app.networkHandler))
	ni := n.PathPrefix("/interfaces").Subrouter()
	ni.Handle("", appHandler(app.trailingSlashRedirect))
	ni.Handle("/", appHandler(app.networkInterfacesHandler))
	nim := ni.PathPrefix("/macs").Subrouter()
	nim.Handle("", appHandler(app.trailingSlashRedirect))
	nim.Handle("/", appHandler(app.networkInterfacesMacsHandler))
	nimaddr := nim.PathPrefix("/" + app.MacAddress).Subrouter()
	nimaddr.Handle("", appHandler(app.trailingSlashRedirect))
	nimaddr.Handle("/", appHandler(app.networkInterfacesMacsAddrHandler))
	nimaddr.Handle("/network-interface-id", appHandler(app.nimAddrInterfaceIDHandler))
	nimaddr.Handle("/network-interface-id/", appHandler(app.nimAddrInterfaceIDHandler))
	nimaddr.Handle("/vpc-id", appHandler(app.vpcHandler))
	nimaddr.Handle("/vpc-id/", appHandler(app.vpcHandler))

	m.Handle("/vpc-id", appHandler(app.vpcHandler))
	m.Handle("/zone-id", appHandler(app.availabilityZoneHandler))
	m.Handle("/region-id", appHandler(app.regionIDHandler))

	m.Handle("/hostname", appHandler(app.hostnameHandler))
	m.Handle("/hostname/", appHandler(app.hostnameHandler))

	sr.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	d.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	ii.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	m.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	i.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	r.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	rsc.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	n.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	ni.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	nim.Handle("/{path:.*}", appHandler(app.notFoundHandler))
	nimaddr.Handle("/{path:.*}", appHandler(app.notFoundHandler))
}

type appHandler func(http.ResponseWriter, *http.Request)

func (fn appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Infof("Requesting %s", r.RequestURI)
	w.Header().Set("Server", "ECSws")
	fn(w, r)
}

func (app *App) rootHandler(w http.ResponseWriter, r *http.Request) {
	write(w, strings.Join(app.apiVersionPrefixes(), "\n"))
}

func (app *App) trailingSlashRedirect(w http.ResponseWriter, r *http.Request) {
	location := ""
	if !app.NoSchemeHostRedirects {
		location = "http://100.100.100.200"
	}
	location = fmt.Sprintf("%s%s/", location, r.URL.String())
	w.Header().Set("Location", location)
	w.WriteHeader(301)
}

func (app *App) secondLevelHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `dynamic
meta-data
user-data`)
}

func (app *App) dynamicHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `instance-identity/
`)
}

func (app *App) instanceIdentityHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `document
pkcs7
signature
`)
}

type InstanceIdentityDocument struct {
	InstanceID   string `json:"instance-id"`
	ImageID      string `json:"image-id"`
	InstanceType string `json:"instance-type"`
	AccountID    string `json:"owner-account-id"`
	RegionID     string `json:"region-id"`
	ZoneID       string `json:"zone-id"`
	PrivateIP    string `json:"private-ipv4"`
}

func (app *App) instanceIdentityDocumentHandler(w http.ResponseWriter, r *http.Request) {
	document := InstanceIdentityDocument{
		ZoneID:       app.ZoneID,
		RegionID:     app.ZoneID[:len(app.ZoneID)-2],
		PrivateIP:    app.PrivateIP,
		InstanceID:   app.InstanceID,
		InstanceType: app.InstanceType,
		AccountID:    "123456789012",
		ImageID:      app.ImageID,
	}
	result, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		log.Errorf("Error marshalling json %+v", err)
		http.Error(w, err.Error(), 500)
	}
	write(w, string(result))
}

// https://www.alibabacloud.com/help/zh/doc-detail/67254.htm
func (app *App) instanceIdentityPkcs7Handler(w http.ResponseWriter, r *http.Request) {
	write(w, `PKCS7`)
}

// https://www.alibabacloud.com/help/zh/doc-detail/67254.htm
func (app *App) instanceIdentitySignatureHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `SIGNATURE`)
}

func (app *App) metaDataHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: if IAM Role/Instance Profile is disabled, don't add iam/ to the list (same behavior as real metadata service)
	write(w, `dns-conf/
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
zone-id`)
}

func (app *App) imageIDHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.ImageID)
}

func (app *App) hostnameHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.Hostname)
}

func (app *App) ramHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `security-credentials/`)
}

func (app *App) instanceIDHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.InstanceID)
}

func (app *App) instanceTypeHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.InstanceType)
}

func (app *App) privateIPHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.PrivateIP)
}

func (app *App) macHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.MacAddress)
}

func (app *App) networkHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `interfaces/`)
}

func (app *App) networkInterfacesHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `macs/`)
}

func (app *App) availabilityZoneHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.ZoneID)
}

func (app *App) regionIDHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.RegionID)
}

func (app *App) securityCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.RoleName)
}

func (app *App) networkInterfacesMacsHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.MacAddress+"/")
}

func (app *App) networkInterfacesMacsAddrHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `gateway
netmask
network-interface-id
primary-ip-address
private-ipv4s
vpc-cidr-block
vpc-id
vswitch-cidr-block
vswitch-id`)
}

func (app *App) nimAddrInterfaceIDHandler(w http.ResponseWriter, r *http.Request) {
	write(w, `eni-asdfasdf`)
}

func (app *App) vpcHandler(w http.ResponseWriter, r *http.Request) {
	write(w, app.VpcID)
}

// Credentials represent the security credentials response
type Credentials struct {
	Code            string
	LastUpdated     string
	AccessKeyID     string `json:"AccessKeyId"`
	AccessKeySecret string
	SecurityToken   string
	Expiration      string
}

func (app *App) mockRoleHandler(w http.ResponseWriter, r *http.Request) {
	// TODOLATER: round to nearest hour, to ensure test coverage passes more reliably?
	now := time.Now().UTC()
	expire := now.Add(6 * time.Hour)
	format := "2006-01-02T15:04:05Z"
	write(w, fmt.Sprintf(`{
  "Code" : "Success",
  "LastUpdated" : "%s",
  "AccessKeyId" : "mock-access-key-id",
  "SecretAccessKey" : "mock-secret-access-key",
  "Token" : "mock-token",
  "Expiration" : "%s"
}`, now.Format(format), expire.Format(format)))
}

func (app *App) roleHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("app: %++v", *app)
	var err error
	var svc *sts.Client
	svc, err = sts.NewClientWithStsToken(app.RegionID, app.AccessKeyID, app.AccessKeySecret, app.StsToken)
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
	request := sts.CreateAssumeRoleRequest()
	request.Scheme = "https"
	request.RoleArn = app.RoleArn
	request.RoleSessionName = "aliyun-mock-metadata"
	resp, err := svc.AssumeRole(request)
	if err != nil {
		log.Errorf("Error assuming role %+v", err)
		http.Error(w, err.Error(), 500)
		return
	}
	log.Debugf("STS response %+v", resp)
	credentials := Credentials{
		AccessKeyID:     resp.Credentials.AccessKeyId,
		Code:            "Success",
		Expiration:      resp.Credentials.Expiration,
		LastUpdated:     time.Now().Format("2006-01-02T15:04:05Z"),
		AccessKeySecret: resp.Credentials.AccessKeySecret,
		SecurityToken:   resp.Credentials.SecurityToken,
	}
	if err := json.NewEncoder(w).Encode(credentials); err != nil {
		log.Errorf("Error sending json %+v", err)
		http.Error(w, err.Error(), 500)
	}
}

func (app *App) notFoundHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]
	w.WriteHeader(404)
	write(w, `<?xml version="1.0" encoding="iso-8859-1"?>
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN"
         "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
 <head>
  <title>404 - Not Found</title>
 </head>
 <body>
  <h1>404 - Not Found</h1>
 </body>
</html>`)
	log.Errorf("Not found " + path)
}

func write(w http.ResponseWriter, s string) {
	if _, err := w.Write([]byte(s)); err != nil {
		log.Errorf("Error writing response: %+v", err)
	}
}
