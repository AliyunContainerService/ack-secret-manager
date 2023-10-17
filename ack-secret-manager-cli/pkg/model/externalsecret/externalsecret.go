package externalsecret

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/info"
)

type ExternalSecretModel struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode cursor.Mode
	preModel   model.Model
}

func init() {
	m := initialExternalSecretModel()
	model.InitModelMap["external"] = m
}

func initialExternalSecretModel() *ExternalSecretModel {
	m := &ExternalSecretModel{
		inputs: make([]textinput.Model, 3),
	}

	var t textinput.Model
	for i := range m.inputs {
		t = textinput.New()
		t.Cursor.Style = cursorStyle
		t.CharLimit = 256
		t.PromptStyle = noStyle
		switch i {
		case 0:
			t.Focus()
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
			t.Prompt = "External Secret Name > "
			t.Placeholder = "k8s resource name"
		case 1:
			t.Prompt = "External Secret Namespace > "
			t.Placeholder = "k8s resource name"
		case 2:
			t.Prompt = "Secret Type > "
			t.Placeholder = "Opaque"
		}
		m.inputs[i] = t
	}

	return m
}

func (m *ExternalSecretModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ExternalSecretModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *ExternalSecretModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *ExternalSecretModel) View() string {
	var b strings.Builder

	b.WriteString(inputTitleStyle.Render("Please enter the basic externalsecret data configuration") + "\n")
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

func (m *ExternalSecretModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *ExternalSecretModel) Next() (model.Model, tea.Cmd) {
	if m.focusIndex == len(m.inputs)+1 {
		return m.preModel, nil
	}
	if m.focusIndex == len(m.inputs) {
		tempModel := model.InitModelMap["info"]
		eModel, ok := tempModel.(*info.InfoModel)
		if !ok {
			return m, nil
		}
		if m.inputs[0].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("external secret name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[1].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("external secret namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[2].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("secret type cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		externalSecretName = m.inputs[0].Value()
		externalSecretNamespace = m.inputs[1].Value()
		secretType = m.inputs[2].Value()
		next := model.InitModelMap["data"]
		next.SetPreModel(m)
		return next, nil
	}
	return m, nil
}
