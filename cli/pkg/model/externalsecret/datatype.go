package externalsecret

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model"
)

type DataModel struct {
	list     list.Model
	choice   string
	quitting bool
	preModel model.Model
}

func init() {
	m := initDataModel()
	model.InitModelMap["data"] = m
}
func initDataModel() *DataModel {
	items := []list.Item{
		item("Data"),
		item("DataProcess"),
	}
	items = append(items, item("\n[Previous]"))
	l := list.New(items, itemDelegate{}, defaultWidth, listHeight)
	l.Title = "Please select data source type"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	return &DataModel{
		list: l,
	}
}

func (m *DataModel) Init() tea.Cmd {
	return nil
}

func (m *DataModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *DataModel) View() string {
	return "\n" + m.list.View()
}

func (m *DataModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *DataModel) Next() (model.Model, tea.Cmd) {
	if m.list.Index() == len(m.list.Items())-1 {
		return m.preModel, nil
	}
	next := model.InitModelMap["basic"]
	bModel, ok := next.(*BasicESModel)
	if !ok {
		return m, nil
	}
	if m.choice == "Data" {
		data = append(data, v1alpha1.DataSource{})
		bModel.SetType(false)
	} else {
		process = append(process, v1alpha1.DataProcess{})
		bModel.SetType(true)
	}
	bModel.SetPreModel(m)
	return bModel, nil
}
