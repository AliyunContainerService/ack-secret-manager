package externalsecret

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model"
	"github.com/AliyunContainerService/ack-secret-manager/cli/pkg/model/info"
)

type ReplaceModel struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode cursor.Mode
	preModel   model.Model
}

func init() {
	m := initialReplaceModelModel()
	model.InitModelMap["replace"] = m
}

func initialReplaceModelModel() *ReplaceModel {
	m := &ReplaceModel{
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
			t.Focus()
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
			t.Prompt = "Source > "
			t.Placeholder = "the string that needs to be replaced (supports regular expressions)"
		case 1:
			t.Prompt = "Target > "
			t.Placeholder = "target string when replacing"
		}
		m.inputs[i] = t
	}

	return m
}

func (m *ReplaceModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *ReplaceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

			if m.focusIndex > len(m.inputs)+2 {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs) + 2
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

func (m *ReplaceModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *ReplaceModel) View() string {
	var b strings.Builder

	b.WriteString(inputTitleStyle.Render("Please enter the replace configuration") + "\n")
	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		if i < len(m.inputs)-1 {
			b.WriteRune('\n')
		}
	}

	continueB := &continueButton
	if m.focusIndex == len(m.inputs) {
		continueB = &focusedContinueButton
	}
	button := &blurredButton
	if m.focusIndex == len(m.inputs)+1 {
		button = &focusedButton
	}
	skip := &skipButton
	if m.focusIndex == len(m.inputs)+2 {
		skip = &focusedSkipButton
	}
	fmt.Fprintf(&b, "\n\n%s ", *continueB)
	fmt.Fprintf(&b, "%s ", *button)
	fmt.Fprintf(&b, "%s\n\n ", *skip)

	return b.String()
}

func (m *ReplaceModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *ReplaceModel) Next() (model.Model, tea.Cmd) {
	tempModel := model.InitModelMap["info"]
	eModel, ok := tempModel.(*info.InfoModel)
	if !ok {
		return m, nil
	}
	if m.focusIndex == len(m.inputs) || m.focusIndex == len(m.inputs)+1 {
		if m.inputs[0].Value() == "" {
			eModel.SetInfo("source string cannot be empty")
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[1].Value() == "" {
			eModel.SetInfo("target string cannot be empty")
			eModel.SetPreModel(m)
			return eModel, nil
		}
		processIndex := len(process) - 1
		if process[processIndex].ReplaceKey == nil {
			process[processIndex].ReplaceKey = make([]v1alpha1.ReplaceRule, 0)
		}
		process[processIndex].ReplaceKey = append(process[processIndex].ReplaceKey, v1alpha1.ReplaceRule{
			Source: m.inputs[0].Value(),
			Target: m.inputs[1].Value(),
		})
		if m.focusIndex == len(m.inputs)+1 {
			model.InitModelMap["secret-store-ref"] = InitSecretStoreRefModel()
			next := model.InitModelMap["secret-store-ref"]
			sModel, ok := next.(*SecretStoreRefModel)
			if !ok {
				return m, nil
			}
			sModel.SetPreModel(m)
			sModel.SetType(true)
			return sModel, nil
		} else {
			for i, input := range m.inputs {
				input.Reset()
				m.inputs[i] = input
			}
		}
		return m, nil
	}
	if m.focusIndex == len(m.inputs)+2 {
		model.InitModelMap["secret-store-ref"] = InitSecretStoreRefModel()
		next := model.InitModelMap["secret-store-ref"]
		sModel, ok := next.(*SecretStoreRefModel)
		if !ok {
			return m, nil
		}
		sModel.SetPreModel(m)
		sModel.SetType(true)
		return sModel, nil
	}
	return m, nil
}
