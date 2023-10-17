package info

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
)

type InfoModel struct {
	info     string
	preModel model.Model
}

func init() {
	m := initialModel()
	model.InitModelMap["info"] = m
}

func initialModel() *InfoModel {
	m := &InfoModel{}
	return m
}

func (m *InfoModel) SetInfo(info string) {
	m.info = info
}

func (m *InfoModel) Init() tea.Cmd {
	return nil
}

func (m *InfoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		// Set focus to next input
		case "tab", "shift+tab", "enter", "up", "down":
			return m.Next()
		}
	}
	return m, nil
}

func (m *InfoModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.info) + "\n")
	b.WriteString(helpStyle.Render("Press \"Enter\" to continue"))
	return b.String()
}

func (m *InfoModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *InfoModel) Next() (model.Model, tea.Cmd) {
	if m.preModel == nil {
		return m, tea.Quit
	}
	return m.preModel, nil
}
