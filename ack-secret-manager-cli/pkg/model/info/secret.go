package info

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
)

type SecretModel struct {
	focusIndex      int
	inputs          []textinput.Model
	cursorMode      cursor.Mode
	preModel        model.Model
	secretName      string
	secretNamespace string
	secretKey       string
	needLoad        bool
}

func init() {
	m := initialSecretModel()
	model.InitModelMap["secret"] = m
}

func initialSecretModel() *SecretModel {
	m := &SecretModel{
		inputs: make([]textinput.Model, 1),
	}

	var t textinput.Model
	for i := range m.inputs {
		t = textinput.New()
		t.Cursor.Style = cursorStyle
		t.CharLimit = 256
		t.PromptStyle = noStyle
		switch i {
		case 0:
			//t.Prompt = "OIDC Provider Arn > "
			//t.Placeholder = OidcProviderARNExample
			t.Focus()
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
			t.EchoMode = textinput.EchoPassword
			t.EchoCharacter = '•'
		}

		m.inputs[i] = t
	}

	return m
}

func (m *SecretModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *SecretModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *SecretModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *SecretModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("The corresponding secret does not exist in the cluster, a new secret will be created, please fill in a new value") + "\n")
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

func (m *SecretModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *SecretModel) Next() (model.Model, tea.Cmd) {
	if m.focusIndex == len(m.inputs)+1 {
		return m.preModel, nil
	}
	if m.focusIndex == len(m.inputs) {
		tempModel := model.InitModelMap["info"]
		eModel, ok := tempModel.(*InfoModel)
		if !ok {
			return m, nil
		}
		if m.inputs[0].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("%v cannot be empty", m.inputs[0].Prompt))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		var value string
		value = m.inputs[0].Value()
		if m.needLoad {
			str, err := os.ReadFile(value)
			if err != nil {
				eModel.SetInfo(err.Error())
				eModel.SetPreModel(m)
				return eModel, nil
			}
			value = string(str)
		}
		err := k8s.CreateOrUpdateSecret(m.secretName, m.secretNamespace, m.secretKey, value)
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		eModel.SetInfo(fmt.Sprintf("create secret key %s/%s/%s success", m.secretNamespace, m.secretName, m.secretKey))
		eModel.SetPreModel(m.preModel)
		m.inputs[0].Reset()
		return eModel, nil
	}
	return m, nil
}

func (m *SecretModel) SetInfo(info, name, namespace, key string, load bool) {
	m.inputs[0].Prompt = info
	m.secretNamespace = namespace
	m.secretName = name
	m.secretKey = key
	m.needLoad = load
}
