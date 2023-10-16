package input

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model/info"
	"github.com/AliyunContainerService/ack-secret-manager/cli/utils"
)

type CrossModel struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode cursor.Mode
	preModel   model.Model
	name       string
	namespace  string
}

func init() {
	m := initialCrossModel()
	model.InitModelMap["cross"] = m
}

func initialCrossModel() *CrossModel {
	m := &CrossModel{
		inputs: make([]textinput.Model, 2),
	}

	var t textinput.Model
	for i := range m.inputs {
		t = textinput.New()
		t.Cursor.Style = cursorStyle
		t.CharLimit = 256
		t.PromptStyle = noStyle
		switch i {
		case 0:
			t.Prompt = "Remote RAM Role Arn > "
			t.Placeholder = RamRoleArnExample
			t.Focus()
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
		case 1:
			t.Prompt = "Remote Role Session Name > "
			t.Placeholder = "role session name"
		}
		m.inputs[i] = t
	}

	return m
}

func (m *CrossModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *CrossModel) SetInfo(name, namespace string) {
	m.name = name
	m.namespace = namespace
}
func (m *CrossModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		// Set focus to next input
		case "tab", "shift+tab", "enter", "up", "down":
			s := msg.String()
			if s == "enter" {
				return m.Next()
			}
			// Cycle indexes
			if s == "up" || s == "shift+tab" {
				m.focusIndex--
			} else {
				m.focusIndex++
			}

			if m.focusIndex > len(m.inputs)+1 {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs) + 1
			}

			cmds := make([]tea.Cmd, len(m.inputs))
			for i := 0; i <= len(m.inputs)-1; i++ {
				if i == m.focusIndex {
					// Set focused state
					cmds[i] = m.inputs[i].Focus()
					m.inputs[i].PromptStyle = focusedStyle
					m.inputs[i].TextStyle = focusedStyle
					continue
				}
				// Remove focused state
				m.inputs[i].Blur()
				m.inputs[i].PromptStyle = noStyle
				m.inputs[i].TextStyle = noStyle
			}

			return m, tea.Batch(cmds...)
		}
	}

	// Handle character input and blinking
	cmd := m.updateInputs(msg)

	return m, cmd
}

func (m *CrossModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *CrossModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Please enter the cross-account configuration") + "\n")
	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		if i < len(m.inputs)-1 {
			b.WriteRune('\n')
		}
	}

	button := &blurredButton
	if m.focusIndex == len(m.inputs) {
		button = &focusedButton
	}
	previous := &previousButton
	if m.focusIndex == len(m.inputs)+1 {
		previous = &focusedPreviousButton
	}
	fmt.Fprintf(&b, "\n\n%s ", *button)
	fmt.Fprintf(&b, "%s \n\n", *previous)
	return b.String()
}

func (m *CrossModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *CrossModel) Next() (model.Model, tea.Cmd) {
	if m.focusIndex == len(m.inputs)+1 {
		return m.preModel, nil
	}
	if m.focusIndex == len(m.inputs) {
		nextModel := model.InitModelMap["info"]
		eModel, ok := nextModel.(*info.InfoModel)
		if !ok {
			return m, nil
		}
		if !utils.IsMatchRegex(RamRoleArnRegex, m.inputs[0].Value()) {
			eModel.SetInfo(fmt.Sprintf("ram role arn fmt error, must be %v", RamRoleArnExample))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[1].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("remote role session name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		err := k8s.GetSecretStoreAndUpdate(m.name, m.namespace, m.inputs[0].Value(), m.inputs[1].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		eModel.SetInfo(fmt.Sprintf("update SecretStore %s/%s success", m.namespace, m.name))
		eModel.SetPreModel(nil)
		return eModel, nil
	}

	return m, nil
}
