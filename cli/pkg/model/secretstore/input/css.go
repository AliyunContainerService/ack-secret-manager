package input

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var (
	focusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).PaddingLeft(4)
	cursorStyle  = focusedStyle.Copy()
	titleStyle   = lipgloss.NewStyle().PaddingBottom(1).PaddingTop(1).PaddingLeft(2)
	noStyle      = lipgloss.NewStyle().PaddingLeft(4)

	focusedButton         = focusedStyle.Copy().Render("[ Submit ]")
	focusedPreviousButton = focusedStyle.Copy().Render("[ Previous ]")
	blurredButton         = fmt.Sprintf("%s", noStyle.Render("[ Submit ]"))
	previousButton        = fmt.Sprintf("%s", noStyle.Render("[ Previous ]"))
)
