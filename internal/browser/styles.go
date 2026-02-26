package browser

import "github.com/charmbracelet/lipgloss"

var (
	styleHeader   = lipgloss.NewStyle().Bold(true)
	styleCursor   = lipgloss.NewStyle().Bold(true)
	styleHelp     = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Background(lipgloss.Color("237")) // dark gray
	styleMeta     = lipgloss.NewStyle().Faint(true)
	styleMessage  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	styleFilter   = lipgloss.NewStyle().Bold(true)
)
