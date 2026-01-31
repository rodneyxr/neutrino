package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	docStyle          = lipgloss.NewStyle().Margin(1, 2)
	titleStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF75B5")).Bold(true)
	filterStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#61AFEF"))
	chartFilterStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#98C379"))
	selectedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E06C75")).Bold(true)
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("#E06C75"))
	footerStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).MarginTop(1)
	helpStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Italic(true)

	// Detail styles
	codeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ABB2BF"))
	locationStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#56B6C2"))
	kindStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#C678DD"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5C07B")).Bold(true).Underline(true)
)
