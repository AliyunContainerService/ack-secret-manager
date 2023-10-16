package externalsecret

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	listHeight   = 14
	defaultWidth = 200
)

var (
	titleStyle        = lipgloss.NewStyle().MarginLeft(0)
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(4).Foreground(lipgloss.Color("170"))
	paginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle         = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)

	focusedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).PaddingLeft(4)
	cursorStyle     = focusedStyle.Copy()
	inputTitleStyle = lipgloss.NewStyle().PaddingBottom(1).PaddingTop(1).PaddingLeft(2)
	noStyle         = lipgloss.NewStyle().PaddingLeft(4)

	focusedButton         = focusedStyle.Copy().Render("[ Submit ]")
	focusedPreviousButton = focusedStyle.Copy().Render("[ Previous ]")
	focusedContinueButton = focusedStyle.Copy().Render("[ Continue ]")
	focusedSkipButton     = focusedStyle.Copy().Render("[ Skip ]")
	blurredButton         = fmt.Sprintf("%s", noStyle.Render("[ Submit ]"))
	previousButton        = fmt.Sprintf("%s", noStyle.Render("[ Previous ]"))
	continueButton        = fmt.Sprintf("%s", noStyle.Render("[ Continue ]"))
	skipButton            = fmt.Sprintf("%s", noStyle.Render("[ Skip ]"))
)

type item string

func (i item) FilterValue() string { return "" }

type itemDelegate struct{}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	str := fmt.Sprintf("%s", i)

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string {
			if index == len(m.Items())-1 {
				if len(s) > 0 {
					return selectedItemStyle.Render(strings.Join(s, " "))
				}
			}
			return selectedItemStyle.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}
