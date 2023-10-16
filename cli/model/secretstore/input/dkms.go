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
)

type DKMSModel struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode cursor.Mode
	preModel   model.Model
}

func init() {
	m := initialDKMSModel()
	model.InitModelMap["dkms"] = m
}

func initialDKMSModel() *DKMSModel {
	m := &DKMSModel{
		inputs: make([]textinput.Model, 10),
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
			t.Prompt = "DKMS InstanceID > "
			t.Placeholder = "kst-xxxxxxxxxxxxxxxxxxxxx"
		case 1:
			t.Prompt = "ClientKey SecretRef Name > "
			t.Placeholder = "the name of the secret that stores the client key"
		case 2:
			t.Prompt = "ClientKey SecretRef Namespace > "
			t.Placeholder = "the namespace of the secret that stores the client key"
		case 3:
			t.Prompt = "ClientKey SecretRef key > "
			t.Placeholder = "the key of the secret that stores the client key"
		case 4:
			t.Prompt = "Password SecretRef Name > "
			t.Placeholder = "the name of the secret that stores the password"
		case 5:
			t.Prompt = "Password SecretRef Namespace > "
			t.Placeholder = "the namespace of the secret that stores the password"
		case 6:
			t.Prompt = "Password SecretRef key > "
			t.Placeholder = "the key of the secret that stores the password"
		case 7:
			t.Prompt = "SecretStore Name > "
			t.Placeholder = "k8s resource name"
		case 8:
			t.Prompt = "SecretStore Namespace > "
			t.Placeholder = "k8s resource name"
		case 9:
			t.Prompt = "CA Cert > "
			t.Placeholder = "CA Cert Content(optional, base64 encoding)"
			t.CharLimit = 10000
		}

		m.inputs[i] = t
	}
	return m
}

func (m *DKMSModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *DKMSModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *DKMSModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *DKMSModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Please enter the ak configuration") + "\n")
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

func (m *DKMSModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *DKMSModel) Next() (model.Model, tea.Cmd) {
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
			eModel.SetInfo(fmt.Sprintf("DKMS InstanceID cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[1].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("ClientKey SecretRef Name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[2].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("ClientKey SecretRef Namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[3].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("ClientKey SecretRef key cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[4].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("Password SecretRef Name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[5].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("Password SecretRef Namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[6].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("Password SecretRef key cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[7].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SecretStore name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[8].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SecretStore namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		exist, err := k8s.CheckSecretKeyExist(m.inputs[1].Value(), m.inputs[2].Value(), m.inputs[3].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if !exist {
			sModel := model.InitModelMap["secret"]
			secretModel, ok := sModel.(*info.SecretModel)
			if !ok {
				return m, nil
			}
			secretModel.SetInfo("Client Key File Path > ", m.inputs[1].Value(), m.inputs[2].Value(), m.inputs[3].Value(), true)
			secretModel.SetPreModel(m)
			return secretModel, nil
		}
		exist, err = k8s.CheckSecretKeyExist(m.inputs[4].Value(), m.inputs[5].Value(), m.inputs[6].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if !exist {
			sModel := model.InitModelMap["secret"]
			secretModel, ok := sModel.(*info.SecretModel)
			if !ok {
				return m, nil
			}
			secretModel.SetInfo("Password > ", m.inputs[4].Value(), m.inputs[5].Value(), m.inputs[6].Value(), false)
			secretModel.SetPreModel(m)
			return secretModel, nil
		}
		err = k8s.CreateDKMSSecretStore(m.inputs[7].Value(), m.inputs[8].Value(), m.inputs[1].Value(),
			m.inputs[2].Value(), m.inputs[3].Value(), m.inputs[4].Value(),
			m.inputs[5].Value(), m.inputs[6].Value(), m.inputs[9].Value(), m.inputs[0].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		eModel.SetInfo(fmt.Sprintf("create SecretStore %s/%s success", m.inputs[8].Value(), m.inputs[7].Value()))
		eModel.SetPreModel(nil)
		return eModel, nil
	}
	return m, nil
}
