package list

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/secretstore/input"
)

const (
	limit = 5
)

type CrossModel struct {
	list          list.Model
	choice        string
	quitting      bool
	preModel      model.Model
	continueToken string
}

func InitCrossModel() *CrossModel {
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
	items = append(items, item("\n[Previous]"))
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.Title = "Please select an existing SecretStore for cross-account configuration"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &CrossModel{
		list:          l,
		continueToken: continueToken,
	}
}

func (m *CrossModel) Init() tea.Cmd {
	return nil
}

func (m *CrossModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *CrossModel) View() string {
	return "\n" + m.list.View()
}

func (m *CrossModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *CrossModel) Next() (model.Model, tea.Cmd) {
	if m.list.Index() == len(m.list.Items())-1 {
		model.InitModelMap["cross-choose"] = InitCrossModel()
		return m.preModel, nil
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
		items = append(items, item("\n[Previous]"))
		m.list.SetItems(items)
		m.continueToken = continueToken
		return m, nil
	}
	next := model.InitModelMap["cross"]
	crossModel, ok := next.(*input.CrossModel)
	if !ok {
		return m, nil
	}
	names := strings.Split(strings.Split(m.choice, " ")[1], "/")
	crossModel.SetInfo(names[1], names[0])
	crossModel.SetPreModel(m)
	return crossModel, nil
}
