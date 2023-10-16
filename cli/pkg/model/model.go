package model

import tea "github.com/charmbracelet/bubbletea"

type Model interface {
	// bubbletea
	Init() tea.Cmd
	Update(msg tea.Msg) (tea.Model, tea.Cmd)
	View() string

	Next() (Model, tea.Cmd)
	SetPreModel(model Model)
}

var InitModelMap = make(map[string]Model)
