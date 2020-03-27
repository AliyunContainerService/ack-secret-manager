package main

import (
	"os"
	"runtime"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

// App encapsulates all of the parameters necessary for starting up
// an Alibaba Cloud mock metadata server. These can either be set via command line or directly.
type App struct {
	ImageID      string
	ZoneID       string
	AppPort      string
	Hostname     string
	InstanceID   string
	InstanceType string
	MacAddress   string
	PrivateIP    string
	// If set, will return mocked credentials to the IAM instance profile instead of using STS to retrieve real credentials.
	MockInstanceProfile   bool
	RoleArn               string
	RoleName              string
	Verbose               bool
	VpcID                 string
	NoSchemeHostRedirects bool
	RegionID              string
	AccessKeyID           string
	AccessKeySecret       string
	StsToken              string
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	app := &App{}
	app.addFlags(pflag.CommandLine)
	pflag.Parse()

	if app.Verbose {
		log.SetLevel(log.DebugLevel)
	}

	app.AccessKeyID = os.Getenv("ACCESS_KEY_ID")
	app.AccessKeySecret = os.Getenv("ACCESS_KEY_SECRET")
	app.StsToken = os.Getenv("SECURITY_TOKEN")
	app.RegionID = app.ZoneID[:len(app.ZoneID)-2]
	app.StartServer()
}

func (app *App) addFlags(fs *pflag.FlagSet) {
	fs.StringVar(&app.ImageID, "image-id", app.ImageID, "ECS Instance image ID")
	fs.StringVar(&app.ZoneID, "zone-id", app.ZoneID, "Availability Zone ID")
	fs.StringVar(&app.AppPort, "app-port", app.AppPort, "HTTP Port")
	fs.StringVar(&app.Hostname, "hostname", app.Hostname, "ECS Instance Hostname")
	fs.StringVar(&app.InstanceID, "instance-id", app.InstanceID, "ECS Instance ID")
	fs.StringVar(&app.InstanceType, "instance-type", app.InstanceType, "ECS Instance Type")
	fs.StringVar(&app.MacAddress, "mac-address", app.MacAddress, "ECS MAC Address")
	fs.StringVar(&app.PrivateIP, "private-ip", app.PrivateIP, "ECS Private IP")
	fs.BoolVar(&app.MockInstanceProfile, "mock-instance-profile", false, "Use mocked RAM Instance Profile credentials (instead of STS generated credentials)")
	fs.StringVar(&app.RoleArn, "role-arn", app.RoleArn, "RAM Role ARN")
	fs.StringVar(&app.RoleName, "role-name", app.RoleName, "RAM Role Name")
	fs.BoolVar(&app.Verbose, "verbose", false, "Verbose")
	fs.StringVar(&app.VpcID, "vpc-id", app.VpcID, "VPC ID")
	fs.BoolVar(&app.NoSchemeHostRedirects, "no-scheme-host-redirects", app.NoSchemeHostRedirects, "Disable the scheme://host prefix in Location redirect headers")
}
