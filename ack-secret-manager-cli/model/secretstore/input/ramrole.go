package input

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/k8s"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model"
	info2 "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/model/info"
	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/utils"
)

type RoleModel struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode cursor.Mode
	preModel   model.Model
}

func init() {
	m := initialRAMRoleModel()
	model.InitModelMap["role"] = m
}

func initialRAMRoleModel() *RoleModel {
	m := &RoleModel{
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
			t.Prompt = "AK SecretRef Name > "
			t.Placeholder = "the name of the secret that stores the access key"
		case 1:
			t.Prompt = "AK SecretRef Namespace > "
			t.Placeholder = "the namespace of the secret that stores the access key"
		case 2:
			t.Prompt = "AK SecretRef Key > "
			t.Placeholder = "the key of the secret key that stores the access key"
		case 3:
			t.Prompt = "SK SecretRef Name > "
			t.Placeholder = "the name of the secret that stores the access key secret"
		case 4:
			t.Prompt = "SK SecretRef Namespace > "
			t.Placeholder = "the namespace of the secret that stores the access key secret"
		case 5:
			t.Prompt = "SK SecretRef Key > "
			t.Placeholder = "the key of the secret that stores the access key secret"
		case 6:
			t.Prompt = "RAM Role Arn > "
			t.Placeholder = RamRoleArnExample
		case 7:
			t.Prompt = "Role Session Name > "
			t.Placeholder = "role session name"
		case 8:
			t.Prompt = "SecretStore Name > "
			t.Placeholder = "k8s resource name"
		case 9:
			t.Prompt = "SecretStore Namespace > "
			t.Placeholder = "k8s resource name"
		}
		m.inputs[i] = t
	}

	return m
}

func (m *RoleModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *RoleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *RoleModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m *RoleModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Please enter the ram role configuration") + "\n")
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

func (m *RoleModel) SetPreModel(preModel model.Model) {
	m.preModel = preModel
}

func (m *RoleModel) Next() (model.Model, tea.Cmd) {
	if m.focusIndex == len(m.inputs)+1 {
		return m.preModel, nil
	}
	if m.focusIndex == len(m.inputs) {
		tempModel := model.InitModelMap["info"]
		eModel, ok := tempModel.(*info2.InfoModel)
		if !ok {
			return m, nil
		}
		if m.inputs[0].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("AK SecretRef name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[1].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("AK SecretRef namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[2].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("AK SecretRef key cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[3].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SK SecretRef name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[4].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SK SecretRef namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[5].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SK SecretRef key cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if !utils.IsMatchRegex(RamRoleArnRegex, m.inputs[6].Value()) {
			eModel.SetInfo(fmt.Sprintf("ram role arn fmt error, must be %v", RamRoleArnExample))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[7].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("role session name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if m.inputs[8].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SecretStore name cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}

		if m.inputs[9].Value() == "" {
			eModel.SetInfo(fmt.Sprintf("SecretStore namespace cannot be empty"))
			eModel.SetPreModel(m)
			return eModel, nil
		}
		exist, err := k8s.CheckSecretKeyExist(m.inputs[0].Value(), m.inputs[1].Value(), m.inputs[2].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if !exist {
			sModel := model.InitModelMap["secret"]
			secretModel, ok := sModel.(*info2.SecretModel)
			if !ok {
				return m, nil
			}
			secretModel.SetInfo("Access Key > ", m.inputs[0].Value(), m.inputs[1].Value(), m.inputs[2].Value(), false)
			secretModel.SetPreModel(m)
			return secretModel, nil
		}
		exist, err = k8s.CheckSecretKeyExist(m.inputs[3].Value(), m.inputs[4].Value(), m.inputs[5].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		if !exist {
			sModel := model.InitModelMap["secret"]
			secretModel, ok := sModel.(*info2.SecretModel)
			if !ok {
				return m, nil
			}
			secretModel.SetInfo("Access Key Secret > ", m.inputs[3].Value(), m.inputs[4].Value(), m.inputs[5].Value(), false)
			secretModel.SetPreModel(m)
			return secretModel, nil
		}
		err = k8s.CreateSecretStoreByAKSK(m.inputs[8].Value(), m.inputs[9].Value(), m.inputs[0].Value(),
			m.inputs[1].Value(), m.inputs[2].Value(), m.inputs[3].Value(),
			m.inputs[4].Value(), m.inputs[5].Value(), m.inputs[6].Value(), m.inputs[7].Value())
		if err != nil {
			eModel.SetInfo(err.Error())
			eModel.SetPreModel(m)
			return eModel, nil
		}
		eModel.SetInfo(fmt.Sprintf("create SecretStore %s/%s success", m.inputs[9].Value(), m.inputs[8].Value()))
		eModel.SetPreModel(nil)
		return eModel, nil
	}
	return m, nil
}
