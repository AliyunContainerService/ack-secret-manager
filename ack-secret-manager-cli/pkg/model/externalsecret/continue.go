package externalsecret

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/info"
)

type ContinueModel struct {
	list     list.Model
	choice   string
	quitting bool
	preModel model.Model
}

func init() {
	m := initContinueModel()
	model.InitModelMap["continue"] = m
}
func initContinueModel() *ContinueModel {
	items := []list.Item{
		item("Yes"),
		item("No"),
	}
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &ContinueModel{
		list: l,
	}
}

func (m *ContinueModel) Init() tea.Cmd {
	return nil
}

func (m *ContinueModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *ContinueModel) View() string {
	return "\n" + m.list.View()
}

func (m *ContinueModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *ContinueModel) SetTitle(title string) {
	m.list.Title = title
}

func (m *ContinueModel) Next() (model.Model, tea.Cmd) {
	tempModel := model.InitModelMap["info"]
	eModel, ok := tempModel.(*info.InfoModel)
	if !ok {
		return m, nil
	}
	if m.choice == "Yes" {
		next := model.InitModelMap["data"]
		next.SetPreModel(m)
		return next, nil
	} else {
		err := k8s.CreateExternalSecret(data, process, externalSecretName, externalSecretNamespace, secretType)
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(nil)
			return eModel, nil
		}
		eModel.SetInfo(fmt.Sprintf("create externalsecret %s/%s success", externalSecretNamespace, externalSecretName))
		eModel.SetPreModel(nil)
		return eModel, nil
	}
}
