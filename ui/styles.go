package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors
	ColorPrimary   = lipgloss.Color("#7D56F4") // Purple
	ColorSecondary = lipgloss.Color("#EC4899") // Pink
	ColorSuccess   = lipgloss.Color("#10B981") // Green
	ColorWarning   = lipgloss.Color("#F59E0B") // Yellow
	ColorDanger    = lipgloss.Color("#EF4444") // Red
	ColorNeon      = lipgloss.Color("#CCFF00") // Neon Yellow
	ColorText      = lipgloss.Color("#F8FAFC") // Light Gray
	ColorTextMuted = lipgloss.Color("#64748B") // Slate
	ColorBgDark    = lipgloss.Color("#0F172A") // Dark Slate
	ColorBgPanel   = lipgloss.Color("#1E293B") // Slightly lighter slate

	// Styles
	StyleTitle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			MarginBottom(1)

	StyleHeader = lipgloss.NewStyle().
			Foreground(ColorText).
			Bold(true).
			Padding(0, 1).
			Background(ColorPrimary)

	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(1, 2)

	StyleActiveRow = lipgloss.NewStyle().
			Background(lipgloss.Color("#334155")).
			Foreground(ColorText)

	StyleNormalRow = lipgloss.NewStyle().
			Foreground(ColorText)

	StyleStatusUp   = lipgloss.NewStyle().Foreground(ColorSuccess)
	StyleStatusDown = lipgloss.NewStyle().Foreground(ColorDanger)

	StyleHelp = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			MarginTop(1)
)

func DrawProgressBar(percent float64, width int) string {
	if width < 5 {
		width = 5
	}
	activeChars := int((percent / 100.0) * float64(width))
	if activeChars > width {
		activeChars = width
	}
	if activeChars < 0 {
		activeChars = 0
	}

	inactiveChars := width - activeChars

	activeColor := ColorSuccess
	if percent > 85 {
		activeColor = ColorDanger
	} else if percent > 60 {
		activeColor = ColorWarning
	}

	activeStyle := lipgloss.NewStyle().Foreground(activeColor)
	inactiveStyle := lipgloss.NewStyle().Background(ColorBgPanel).Foreground(ColorBgPanel)

	blockChar := "█"
	emptyChar := " "

	activeStr := ""
	for i := 0; i < activeChars; i++ {
		activeStr += blockChar
	}
	inactiveStr := ""
	for i := 0; i < inactiveChars; i++ {
		inactiveStr += emptyChar
	}

	return "[" + activeStyle.Render(activeStr) + inactiveStyle.Render(inactiveStr) + "]"
}
