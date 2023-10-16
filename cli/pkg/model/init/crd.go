package list

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model"
)

func init() {
	crdModel := InitCRDModel()
	model.InitModelMap["crd"] = crdModel
}

type CrdModel struct {
	list     list.Model
	choice   string
	quitting bool
	preModel model.Model
}

func InitCRDModel() *CrdModel {
	items := []list.Item{
		item("SecretStore"),
		item("ExternalSecret"),
		item("\n[Exit]"),
	}
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.Title = "Please select the CRD you want to create"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &CrdModel{
		list: l,
	}
}

func (m *CrdModel) Init() tea.Cmd {
	return nil
}

func (m *CrdModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *CrdModel) View() string {
	return "\n" + m.list.View()
}

func (m *CrdModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *CrdModel) Next() (model.Model, tea.Cmd) {
	var next model.Model
	if m.choice == "SecretStore" {
		next = model.InitModelMap["authentication"]
	} else if m.choice == "ExternalSecret" {
		next = model.InitModelMap["external"]
	} else {
		return m, tea.Quit
	}
	next.SetPreModel(m)
	return next, nil
}

//m := InitCRDModel()
//	if _, err := tea.NewProgram(m).Run(); err != nil {
//		fmt.Println("Error running program:", err)
//		os.Exit(1)
//	}
