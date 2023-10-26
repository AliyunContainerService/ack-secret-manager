package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/externalsecret"
	_ "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/externalsecret"
	_ "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/info"
	_ "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/init"
	_ "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/secretstore/input"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/secretstore/list"
	_ "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/secretstore/list"
)

var (
	kubeConfigPath string
	defaultPath    string
	helpInfo       bool
)

func main() {
	//ctx := context.Background()
	homeDir, err := os.UserHomeDir()
	if err == nil {
		defaultPath = fmt.Sprintf("%s/%s", homeDir, ".kube/config")
	}
	flag.StringVar(&kubeConfigPath, "kubeconfig", defaultPath, "kubeconfig path")
	flag.BoolVar(&helpInfo, "help", false, "help info")
	flag.Parse()
	if helpInfo {
		flag.Usage()
		return
	}
	err = k8s.InitClient(kubeConfigPath)
	if err != nil {
		panic(err)
	}
	model.InitModelMap["cross-choose"] = list.InitCrossModel()
	model.InitModelMap["secret-store-ref"] = externalsecret.InitSecretStoreRefModel()
	m := model.InitModelMap["crd"]
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
	return
}
