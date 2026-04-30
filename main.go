package main

import (
	"fmt"
	"os"

	"Tuidock/docker"
	"Tuidock/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	ds, _ := docker.NewLocalDockerService()
	// We don't check err strictly here because ui.NewAppModel handles ds == nil 
	// by automatically switching to SSH view or saved hosts.

	m := ui.NewAppModel(ds)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
