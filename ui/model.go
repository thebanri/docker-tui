package ui

import (
	"context"
	"fmt"
	"time"

	"docker-tui/docker"
	"docker-tui/models"
	"docker-tui/ssh"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ViewMode int

const (
	ViewList ViewMode = iota
	ViewSSHForm
	ViewSSHSaved
)

type tickMsg time.Time
type containersMsg []models.ContainerData
type errMsg struct{ err error }

type SSHForm struct {
	inputs  []textinput.Model
	focused int
}

func newSSHForm() SSHForm {
	var inputs []textinput.Model = make([]textinput.Model, 5)

	inputs[0] = textinput.New()
	inputs[0].Placeholder = "Host (e.g. 192.168.1.100)"
	inputs[0].Focus()
	inputs[0].CharLimit = 156
	inputs[0].Width = 40

	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Port (e.g. 22)"
	inputs[1].CharLimit = 5
	inputs[1].Width = 40
	inputs[1].SetValue("22")

	inputs[2] = textinput.New()
	inputs[2].Placeholder = "Username (e.g. root)"
	inputs[2].CharLimit = 64
	inputs[2].Width = 40

	inputs[3] = textinput.New()
	inputs[3].Placeholder = "Password (Leave empty if using key)"
	inputs[3].EchoMode = textinput.EchoPassword
	inputs[3].EchoCharacter = '•'
	inputs[3].CharLimit = 128
	inputs[3].Width = 40

	inputs[4] = textinput.New()
	inputs[4].Placeholder = "Private Key (Path like ~/.ssh/id_rsa or raw content)"
	inputs[4].CharLimit = 8192 // Can be very long if content
	inputs[4].Width = 40

	return SSHForm{inputs: inputs, focused: 0}
}

type AppModel struct {
	dockerService docker.Service
	state         models.AppState
	mode          ViewMode
	sshForm       SSHForm
	width         int
	height        int
	cursor        int
	savedCursor   int
}

func NewAppModel(ds docker.Service) *AppModel {
	return &AppModel{
		dockerService: ds,
		state: models.AppState{
			ConnectionType: models.LocalConnection,
			ServerName:     "localhost",
		},
		mode:    ViewList,
		sshForm: newSSHForm(),
	}
}

func (m *AppModel) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.tickCmd(),
		m.fetchContainersCmd(),
	)
}

func (m *AppModel) tickCmd() tea.Cmd {
	return tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *AppModel) fetchContainersCmd() tea.Cmd {
	return func() tea.Msg {
		if m.dockerService == nil {
			return errMsg{fmt.Errorf("not connected to any docker daemon")}
		}
		containers, err := m.dockerService.GetContainers()
		if err != nil {
			return errMsg{err}
		}
		return containersMsg(containers)
	}
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		cmds = append(cmds, m.fetchContainersCmd(), m.tickCmd())

	case containersMsg:
		// Group containers by Project
		grouped := make(map[string]*models.ContainerData)
		var orderedProjects []string

		for _, c := range msg {
			proj := c.Project
			if proj == "" {
				proj = c.Name
			}
			if existing, ok := grouped[proj]; ok {
				existing.CPUPercent += c.CPUPercent
				existing.MemPercent += c.MemPercent
				existing.PIDs += c.PIDs
				// Aggregate block/net IO strings is complex, just mark them as mixed or ignore
				existing.Ports = "..."
				if existing.State != c.State {
					existing.State = "mixed"
				}
				existing.Name = proj + " (" + existing.ID + "...)" // Indicate group
				existing.GroupIDs = append(existing.GroupIDs, c.ID)
			} else {
				orderedProjects = append(orderedProjects, proj)
				copyC := c
				copyC.Name = proj
				copyC.GroupIDs = []string{c.ID}
				grouped[proj] = &copyC
			}
		}

		var aggregated []models.ContainerData
		for _, proj := range orderedProjects {
			aggregated = append(aggregated, *grouped[proj])
		}

		m.state.Containers = aggregated
		m.state.Error = nil
		if m.cursor >= len(m.state.Containers) {
			m.cursor = len(m.state.Containers) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}

	case errMsg:
		m.state.Error = msg.err

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		if m.mode == ViewList {
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.state.Containers)-1 {
					m.cursor++
				}
			case "a":
				if len(m.state.Containers) > 0 {
					c := m.state.Containers[m.cursor]
					err := m.dockerService.StartContainers(context.Background(), c.GroupIDs)
					if err != nil {
						m.state.Error = err
					} else {
						cmds = append(cmds, m.fetchContainersCmd())
					}
				}
			case "x":
				if len(m.state.Containers) > 0 {
					c := m.state.Containers[m.cursor]
					err := m.dockerService.StopContainers(context.Background(), c.GroupIDs)
					if err != nil {
						m.state.Error = err
					} else {
						cmds = append(cmds, m.fetchContainersCmd())
					}
				}
			case "r":
				if len(m.state.Containers) > 0 {
					c := m.state.Containers[m.cursor]
					err := m.dockerService.RestartContainers(context.Background(), c.GroupIDs)
					if err != nil {
						m.state.Error = err
					} else {
						cmds = append(cmds, m.fetchContainersCmd())
					}
				}
			case "s":
				cfg := models.LoadConfig()
				if len(cfg.SavedHosts) > 0 {
					m.mode = ViewSSHSaved
					m.savedCursor = 0
				} else {
					m.mode = ViewSSHForm
				}
			case "l":
				// Switch back to local
				if m.state.ConnectionType != models.LocalConnection {
					if m.dockerService != nil {
						m.dockerService.Close()
					}
					local, err := docker.NewLocalDockerService()
					if err == nil {
						m.dockerService = local
						m.state.ConnectionType = models.LocalConnection
						m.state.ServerName = "localhost"
						cmds = append(cmds, m.fetchContainersCmd())
					} else {
						m.state.Error = err
					}
				}
			}
		} else if m.mode == ViewSSHForm {
			switch msg.String() {
			case "esc":
				m.mode = ViewList
			case "tab", "down":
				m.sshForm.inputs[m.sshForm.focused].Blur()
				m.sshForm.focused = (m.sshForm.focused + 1) % len(m.sshForm.inputs)
				m.sshForm.inputs[m.sshForm.focused].Focus()
			case "shift+tab", "up":
				m.sshForm.inputs[m.sshForm.focused].Blur()
				m.sshForm.focused--
				if m.sshForm.focused < 0 {
					m.sshForm.focused = len(m.sshForm.inputs) - 1
				}
				m.sshForm.inputs[m.sshForm.focused].Focus()
			case "enter":
				// Submit form
				host := m.sshForm.inputs[0].Value()
				port := m.sshForm.inputs[1].Value()
				user := m.sshForm.inputs[2].Value()
				pass := m.sshForm.inputs[3].Value()
				key := m.sshForm.inputs[4].Value()

				// Connect via SSH
				remoteService, err := ssh.NewRemoteDockerService(host, port, user, pass, key)
				if err != nil {
					m.state.Error = err
				} else {
					models.AddHostToConfig(models.SSHHost{
						Host:       host,
						Port:       port,
						Username:   user,
						Password:   pass,
						PrivateKey: key,
					})
					if m.dockerService != nil {
						m.dockerService.Close()
					}
					m.dockerService = remoteService
					m.state.ConnectionType = models.SSHConnection
					m.state.ServerName = fmt.Sprintf("%s@%s", user, host)
					m.mode = ViewList
					cmds = append(cmds, m.fetchContainersCmd())
				}
			}

			// Update all inputs
			for i := range m.sshForm.inputs {
				var cmd tea.Cmd
				m.sshForm.inputs[i], cmd = m.sshForm.inputs[i].Update(msg)
				cmds = append(cmds, cmd)
			}
		} else if m.mode == ViewSSHSaved {
			cfg := models.LoadConfig()
			switch msg.String() {
			case "esc":
				m.mode = ViewList
			case "up", "k":
				if m.savedCursor > 0 {
					m.savedCursor--
				}
			case "down", "j":
				if m.savedCursor < len(cfg.SavedHosts)-1 {
					m.savedCursor++
				}
			case "n":
				m.mode = ViewSSHForm
			case "enter":
				if m.savedCursor >= 0 && m.savedCursor < len(cfg.SavedHosts) {
					host := cfg.SavedHosts[m.savedCursor]
					remoteService, err := ssh.NewRemoteDockerService(host.Host, host.Port, host.Username, host.Password, host.PrivateKey)
					if err != nil {
						m.state.Error = err
					} else {
						if m.dockerService != nil {
							m.dockerService.Close()
						}
						m.dockerService = remoteService
						m.state.ConnectionType = models.SSHConnection
						m.state.ServerName = fmt.Sprintf("%s@%s", host.Username, host.Host)
						m.mode = ViewList
						cmds = append(cmds, m.fetchContainersCmd())
					}
				}
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *AppModel) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	// Header
	headerText := fmt.Sprintf(" 🐳 Docker TUI | %s: %s ", m.state.ConnectionType, m.state.ServerName)
	header := StyleHeader.Render(headerText)

	// Error banner if any
	errStr := ""
	if m.state.Error != nil {
		errStr = lipgloss.NewStyle().Foreground(ColorText).Background(ColorDanger).Padding(0, 1).Render("Error: "+m.state.Error.Error()) + "\n\n"
	}

	content := ""
	if m.mode == ViewList {
		content = m.viewList()
	} else if m.mode == ViewSSHForm {
		content = m.viewSSHForm()
	} else if m.mode == ViewSSHSaved {
		content = m.viewSSHSaved()
	}

	// Footer
	footer := ""
	if m.mode == ViewList {
		footer = StyleHelp.Render(" [↑/↓] Navigate  [a] Start  [x] Stop  [r] Restart  [s] SSH  [l] Local  [q] Quit ")
	} else if m.mode == ViewSSHForm {
		footer = StyleHelp.Render(" [Tab] Next Field  [Enter] Connect  [Esc] Cancel ")
	} else if m.mode == ViewSSHSaved {
		footer = StyleHelp.Render(" [↑/↓] Navigate  [Enter] Connect  [n] New Connection  [Esc] Cancel ")
	}

	layout := lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		errStr+content,
		"",
		footer,
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, layout)
}

func (m *AppModel) viewList() string {
	if len(m.state.Containers) == 0 {
		return StylePanel.Render("No containers found or loading...")
	}

	// Calculate widths
	wName := 25
	wState := 10
	wCPU := 20
	wMem := 20
	wDisk := 15
	wPorts := 20

	headerRow := lipgloss.JoinHorizontal(lipgloss.Left,
		fmt.Sprintf("%-*s", wName, "NAME"),
		fmt.Sprintf("%-*s", wState, "STATE"),
		fmt.Sprintf("%-*s", wCPU, "CPU %"),
		fmt.Sprintf("%-*s", wMem, "RAM %"),
		fmt.Sprintf("%-*s", wDisk, "DISK I/O"),
		fmt.Sprintf("%-*s", wPorts, "PORTS"),
	)
	headerRow = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).Render(headerRow)

	var rows []string
	rows = append(rows, headerRow)

	for i, c := range m.state.Containers {
		name := c.Name
		if len(name) > wName-2 {
			name = name[:wName-5] + "..."
		}

		stateStyle := StyleStatusDown
		if c.State == "running" {
			stateStyle = StyleStatusUp
		} else if c.State == "mixed" {
			stateStyle = lipgloss.NewStyle().Foreground(ColorWarning)
		}

		cpuStr := fmt.Sprintf("%5.1f%% ", c.CPUPercent) + DrawProgressBar(c.CPUPercent, 10)
		memStr := fmt.Sprintf("%5.1f%% ", c.MemPercent) + DrawProgressBar(c.MemPercent, 10)

		rowContent := lipgloss.JoinHorizontal(lipgloss.Left,
			fmt.Sprintf("%-*s", wName, name),
			stateStyle.Render(fmt.Sprintf("%-*s", wState, c.State)),
			fmt.Sprintf("%-*s", wCPU, cpuStr),
			fmt.Sprintf("%-*s", wMem, memStr),
			fmt.Sprintf("%-*s", wDisk, c.BlockIO),
			fmt.Sprintf("%-*s", wPorts, c.Ports),
		)

		if i == m.cursor {
			rows = append(rows, StyleActiveRow.Render(rowContent))
		} else {
			rows = append(rows, StyleNormalRow.Render(rowContent))
		}
	}

	return StylePanel.Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

func (m *AppModel) viewSSHForm() string {
	title := StyleTitle.Render("Add Remote Docker Server (SSH)")

	var inputs []string
	for i := range m.sshForm.inputs {
		inputs = append(inputs, m.sshForm.inputs[i].View())
	}

	form := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		"Host/IP:",
		inputs[0],
		"",
		"Port:",
		inputs[1],
		"",
		"Username:",
		inputs[2],
		"",
		"Password (optional):",
		inputs[3],
		"",
		"Private Key Path/Content (optional):",
		inputs[4],
	)

	return StylePanel.Render(form)
}

func (m *AppModel) viewSSHSaved() string {
	title := StyleTitle.Render("Select Saved SSH Connection")
	cfg := models.LoadConfig()

	var rows []string
	for i, h := range cfg.SavedHosts {
		rowStr := fmt.Sprintf("%s@%s:%s", h.Username, h.Host, h.Port)
		if i == m.savedCursor {
			rows = append(rows, StyleActiveRow.Render("> "+rowStr))
		} else {
			rows = append(rows, StyleNormalRow.Render("  "+rowStr))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, "")
	content = lipgloss.JoinVertical(lipgloss.Left, content, lipgloss.JoinVertical(lipgloss.Left, rows...))

	return StylePanel.Render(content)
}
