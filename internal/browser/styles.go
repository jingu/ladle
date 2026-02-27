package browser

import "github.com/charmbracelet/lipgloss"

var (
	styleHeader       = lipgloss.NewStyle().Bold(true)
	styleCursor       = lipgloss.NewStyle().Bold(true)
	styleHelp         = lipgloss.NewStyle().Faint(true)
	styleSelected     = lipgloss.NewStyle().Background(lipgloss.Color("237")) // dark gray
	styleMeta         = lipgloss.NewStyle().Faint(true)
	styleMessageSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true) // green bold
	styleMessageError   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // red bold
	styleFilter       = lipgloss.NewStyle().Bold(true)
	styleMenuBorder    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	styleMenuSelected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")) // cyan
	styleMenuItem      = lipgloss.NewStyle().Faint(true)
	stylePreviewBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	styleInput         = lipgloss.NewStyle().Bold(true)
)
