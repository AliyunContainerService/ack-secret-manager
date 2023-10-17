package externalsecret

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
)

type SecretStoreRefModel struct {
	list          list.Model
	choice        string
	quitting      bool
	preModel      model.Model
	continueToken string
	isProcess     bool
}

const (
	limit = 5
)

func InitSecretStoreRefModel() *SecretStoreRefModel {
	secretStores, continueToken, err := k8s.ListSecretStore(limit, "")
	if err != nil {
		panic(err)
	}
	items := make([]list.Item, 0)
	for _, secretStore := range secretStores {
		items = append(items, item(fmt.Sprintf("* %s/%s", secretStore.Namespace, secretStore.Name)))
	}
	if len(items) == limit {
		items = append(items, item("Next Page"))
	}
	items = append(items, item("[Previous]"))
	items = append(items, item("[Skip]"))
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.Title = "Please select an existing SecretStore for SecretStoreRef configuration"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &SecretStoreRefModel{
		list:          l,
		continueToken: continueToken,
	}
}

func (m *SecretStoreRefModel) Init() tea.Cmd {
	return nil
}

func (m *SecretStoreRefModel) SetType(isProcess bool) {
	m.isProcess = isProcess
}
func (m *SecretStoreRefModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		switch keypress := msg.String(); keypress {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "enter":
			i, ok := m.list.SelectedItem().(item)
			if ok {
				m.choice = string(i)
			}
			return m.Next()
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *SecretStoreRefModel) View() string {
	return "\n" + m.list.View()
}

func (m *SecretStoreRefModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *SecretStoreRefModel) Next() (model.Model, tea.Cmd) {
	if m.list.Index() == len(m.list.Items())-2 {
		model.InitModelMap["secret-store-ref"] = InitSecretStoreRefModel()
		return m.preModel, nil
	}
	if m.choice == "[Skip]" {
		next := model.InitModelMap["continue"]
		cModel, ok := next.(*ContinueModel)
		if !ok {
			return m, nil
		}
		cModel.SetTitle("Do you want to continue adding data?")
		cModel.SetPreModel(m)
		return cModel, nil
	}
	if m.choice == "Next Page" {
		secretStores, continueToken, err := k8s.ListSecretStore(limit, m.continueToken)
		if err != nil {
			return m, nil
		}
		items := make([]list.Item, 0)
		for _, secretStore := range secretStores {
			items = append(items, item(fmt.Sprintf("* %s/%s", secretStore.Namespace, secretStore.Name)))
		}
		if len(items) == limit {
			items = append(items, item("Next Page"))
		}
		items = append(items, item("[Previous]"))
		m.list.SetItems(items)
		m.continueToken = continueToken
		return m, nil
	}
	names := strings.Split(strings.Split(m.choice, " ")[1], "/")
	if m.isProcess {
		processIndex := len(process) - 1
		process[processIndex].Extract.SecretStoreRef = &v1alpha1.SecretStoreRef{
			Name:      names[1],
			Namespace: names[0],
		}
	} else {
		dataIndex := len(data) - 1
		data[dataIndex].SecretStoreRef = &v1alpha1.SecretStoreRef{
			Name:      names[1],
			Namespace: names[0],
		}
	}
	next := model.InitModelMap["continue"]
	cModel, ok := next.(*ContinueModel)
	if !ok {
		return m, nil
	}
	cModel.SetTitle("Do you want to continue adding data?")
	cModel.SetPreModel(m)
	return cModel, nil
}
