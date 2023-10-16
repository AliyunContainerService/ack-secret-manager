package list

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model"
)

func init() {
	authenticationModel := InitAuthenticationModel()
	model.InitModelMap["authentication"] = authenticationModel
}

type AuthenticationModel struct {
	list     list.Model
	choice   string
	quitting bool
	preModel model.Model
}

func InitAuthenticationModel() *AuthenticationModel {
	items := []list.Item{
		item("RRSA"),
		item("RAM Role"),
		item("AK"),
		item("ClientKey"),
		item("ECS RAM Role"),
		item("Cross-account synchronization"),
		item("\n[ Previous ]"),
	}
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.Title = "Please choose a KMS (Key Management Service) authentication type"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &AuthenticationModel{
		list: l,
	}
}

func (m *AuthenticationModel) Init() tea.Cmd {
	return nil
}

func (m *AuthenticationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *AuthenticationModel) View() string {
	return "\n" + m.list.View()
}

func (m *AuthenticationModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *AuthenticationModel) Next() (model.Model, tea.Cmd) {
	if m.list.Index() == len(m.list.Items())-1 {
		return m.preModel, nil
	}
	var next model.Model
	switch m.choice {
	case "RRSA":
		next = model.InitModelMap["rrsa"]
	case "AK":
		next = model.InitModelMap["ak"]
	case "RAM Role":
		next = model.InitModelMap["role"]
	case "ECS RAM Role":
		next = model.InitModelMap["ecs"]
	case "ClientKey":
		next = model.InitModelMap["dkms"]
	case "Cross-account synchronization":
		next = model.InitModelMap["cross-choose"]
	}
	next.SetPreModel(m)
	return next, nil
}
