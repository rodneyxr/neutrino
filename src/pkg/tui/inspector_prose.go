package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Only explanatory text uses word wrapping. Paths, YAML and input values keep
// their exact characters through wrapCell instead of normalizing whitespace.
func wrapProse(text string, width int) string {
	if width < 1 {
		return ""
	}
	var lines []string
	for _, paragraph := range strings.Split(terminalMultiline(text), "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			parts := strings.Split(wrapCell(word, width), "\n")
			lines = append(lines, parts[:len(parts)-1]...)
			line = parts[len(parts)-1]
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
