package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("#59D5E0")).Bold(true)
	headerStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#E7EDF7")).Bold(true)
	filterStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#59D5E0"))
	helpStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("#78869A"))
	codeStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("#C8D2E1"))
	kindStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("#BDA1FF")).Bold(true)
	zarfVarStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFCA62"))
	mappedValueStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CE36C"))
	excludedValueStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF8793"))
	tableHeaderStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8DA2")).Bold(true)
	snippetStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#59D5E0"))
	fieldLabelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#78869A")).Bold(true)
	paneHeadingStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#AEBACB")).Bold(true)
	componentBandStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CE36C")).Bold(true)
	groupHeadingStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8997AA"))
	selectedRowStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#F4F8FF")).Background(lipgloss.Color("#17303A")).Bold(true)
	udsOverrideValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#59D5E0"))
	udsOverrideVarStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#BDA1FF"))

	headerBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DDE6F3")).
			Background(lipgloss.Color("#0C1119")).
			Padding(0, 1)
	footerBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8997AA")).
			Background(lipgloss.Color("#0C1119")).
			Padding(0, 1)
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#293649")).
			Padding(0, 1)
	focusedPaneStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("#59D5E0")).
				Padding(0, 1)
	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(lipgloss.Color("#59D5E0")).
			Background(lipgloss.Color("#0E151E")).
			Padding(1, 2)
)
