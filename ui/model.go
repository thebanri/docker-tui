package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"Tuidock/docker"
	"Tuidock/models"
	"Tuidock/ssh"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type ViewMode int

const (
	ViewList ViewMode = iota
	ViewSSHForm
	ViewSSHSaved
)

type SortMode int

const (
	SortName SortMode = iota
	SortState
	SortCPU
	SortRAM
)

type tickMsg time.Time
type containersMsg []models.ContainerData
type errMsg struct{ err error }
type restartFinishedMsg struct {
	ids []string
	err error
}

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
	sortMode      SortMode
	sortDesc      bool
	showDetails   bool
	activePanel   int // 0: List, 1: Details
	restartingIDs map[string]bool
	firstFetchDone bool
}

func NewAppModel(ds docker.Service) *AppModel {
	m := &AppModel{
		dockerService: ds,
		state: models.AppState{
			ConnectionType: models.LocalConnection,
			ServerName:     "localhost",
		},
		mode:           ViewList,
		sshForm:        newSSHForm(),
		sortMode:       SortName,
		showDetails:    false,
		activePanel:    0,
		restartingIDs:  make(map[string]bool),
		firstFetchDone: false,
	}

	// If local docker is not available, try to show SSH options immediately
	if ds == nil {
		cfg := models.LoadConfig()
		if len(cfg.SavedHosts) > 0 {
			m.mode = ViewSSHSaved
			m.state.ConnectionType = models.SSHConnection
			m.state.ServerName = "Not Connected"
		} else {
			m.mode = ViewSSHForm
			m.state.ConnectionType = models.SSHConnection
			m.state.ServerName = "New Connection"
		}
	}

	return m
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

func (m *AppModel) applySortAndRestoreCursor(containers []models.ContainerData) {
	// Record currently selected container's Name
	var selectedName string
	if m.cursor >= 0 && m.cursor < len(m.state.Containers) {
		selectedName = m.state.Containers[m.cursor].Name
	}

	sort.Slice(containers, func(i, j int) bool {
		c1, c2 := containers[i], containers[j]
		if m.sortDesc {
			c1, c2 = c2, c1 // invert logic
		}

		switch m.sortMode {
		case SortState:
			if c1.State == c2.State {
				return c1.Name < c2.Name
			}
			return c1.State < c2.State
		case SortCPU:
			if c1.CPUPercent == c2.CPUPercent {
				return c1.Name < c2.Name
			}
			return c1.CPUPercent < c2.CPUPercent
		case SortRAM:
			if c1.MemPercent == c2.MemPercent {
				return c1.Name < c2.Name
			}
			return c1.MemPercent < c2.MemPercent
		case SortName:
			fallthrough
		default:
			return c1.Name < c2.Name
		}
	})

	m.state.Containers = containers

	// Restore cursor
	if selectedName != "" {
		for i, c := range m.state.Containers {
			if c.Name == selectedName {
				m.cursor = i
				break
			}
		}
	} else {
		if m.cursor >= len(m.state.Containers) {
			m.cursor = len(m.state.Containers) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
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

	case restartFinishedMsg:
		for _, id := range msg.ids {
			delete(m.restartingIDs, id)
		}
		if msg.err != nil {
			m.state.Error = msg.err
		}
		cmds = append(cmds, m.fetchContainersCmd())

	case containersMsg:
		m.firstFetchDone = true
		// Clear restartingIDs for containers that are now reported as running/stable
		for _, c := range msg {
			if c.State == "running" {
				delete(m.restartingIDs, c.ID)
			}
		}

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
				existing.SubContainers = append(existing.SubContainers, c)

				// Combine ports instead of hiding them
				if c.Ports != "" {
					portMap := make(map[string]bool)
					if existing.Ports != "" && existing.Ports != "..." {
						for _, p := range strings.Split(existing.Ports, ", ") {
							portMap[p] = true
						}
					}
					for _, p := range strings.Split(c.Ports, ", ") {
						portMap[p] = true
					}
					var merged []string
					for p := range portMap {
						merged = append(merged, p)
					}
					sort.Strings(merged)
					existing.Ports = strings.Join(merged, ", ")
				}

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
				copyC.SubContainers = []models.ContainerData{c}
				grouped[proj] = &copyC
			}
		}

		var aggregated []models.ContainerData
		for _, proj := range orderedProjects {
			pData := grouped[proj]
			// Sort sub-containers by name to keep the list stable in ViewDetails
			sort.Slice(pData.SubContainers, func(i, j int) bool {
				return pData.SubContainers[i].Name < pData.SubContainers[j].Name
			})
			aggregated = append(aggregated, *pData)
		}

		m.applySortAndRestoreCursor(aggregated)
		m.state.Error = nil

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
			case "tab":
				if m.showDetails {
					m.activePanel = (m.activePanel + 1) % 2
				}
			case "enter":
				m.showDetails = !m.showDetails
				if !m.showDetails {
					m.activePanel = 0
				}
			case "up", "k":
				if m.activePanel == 0 {
					if m.cursor > 0 {
						m.cursor--
					}
				}
			case "down", "j":
				if m.activePanel == 0 {
					if m.cursor < len(m.state.Containers)-1 {
						m.cursor++
					}
				}
			case "o":
				m.sortMode = (m.sortMode + 1) % 4
				m.applySortAndRestoreCursor(m.state.Containers)
			case "i":
				m.sortDesc = !m.sortDesc
				m.applySortAndRestoreCursor(m.state.Containers)
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
					// Check if any in group is already restarting
					isRestarting := false
					for _, id := range c.GroupIDs {
						if m.restartingIDs[id] {
							isRestarting = true
							break
						}
					}

					if !isRestarting {
						for _, id := range c.GroupIDs {
							m.restartingIDs[id] = true
						}
						// Run in background so UI doesn't freeze
						return m, func() tea.Msg {
							err := m.dockerService.RestartContainers(context.Background(), c.GroupIDs)
							// We clear them in the next fetch or we could send a specific msg
							// For simplicity, let's just finish the command
							return restartFinishedMsg{ids: c.GroupIDs, err: err}
						}
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
						m.firstFetchDone = false
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
				// Clear previous error before trying
				m.state.Error = nil
				
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
					m.firstFetchDone = false
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
						m.firstFetchDone = false
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
	sortNameStr := "Name"
	switch m.sortMode {
	case SortState:
		sortNameStr = "State"
	case SortCPU:
		sortNameStr = "CPU"
	case SortRAM:
		sortNameStr = "RAM"
	}
	sortDirStr := "Asc"
	if m.sortDesc {
		sortDirStr = "Desc"
	}

	// Segmented Header
	logo := StyleHeaderLogo.Render(" 🐳 Tuidock ")
	connInfo := StyleHeaderInfo.Render(fmt.Sprintf(" %s: %s ", m.state.ConnectionType, m.state.ServerName))
	sortInfo := StyleHeaderSort.Render(fmt.Sprintf(" Sort: %s (%s) ", sortNameStr, sortDirStr))

	// Join pieces with a small gap
	header := lipgloss.JoinHorizontal(lipgloss.Top,
		logo,
		" ",
		connInfo,
		" ",
		sortInfo,
	)

	// Layout sizing and centering logic
	maxUIWidth := 140
	uiWidth := m.width - 4
	if uiWidth > maxUIWidth {
		uiWidth = maxUIWidth
	}

	// Ensure header isn't too wide but matches style
	headerWidth := lipgloss.Width(header)
	if headerWidth < uiWidth {
		// Fill space if needed or keep it segmented
	}

	// Error banner if any
	errStr := ""
	if m.state.Error != nil {
		errStr = lipgloss.NewStyle().Foreground(ColorText).Background(ColorDanger).Padding(0, 1).Render("Error: "+m.state.Error.Error()) + "\n\n"
	}

	var content string
	if m.mode == ViewList {
		if m.showDetails {
			leftWidth := (uiWidth / 2) - 1
			rightWidth := uiWidth - leftWidth - 2

			leftContent := m.viewList(leftWidth)
			rightContent := m.viewDetails(rightWidth)

			leftStyle := StylePanel.Copy().Width(leftWidth)
			rightStyle := StylePanel.Copy().Width(rightWidth)

			if m.activePanel == 0 {
				leftStyle = leftStyle.BorderForeground(ColorPrimary)
				// Dimmer neon when not focused (darker olive/yellow)
				rightStyle = rightStyle.BorderForeground(lipgloss.Color("#667700"))
			} else {
				leftStyle = leftStyle.BorderForeground(ColorBgPanel)
				rightStyle = rightStyle.BorderForeground(ColorNeon)
			}

			content = lipgloss.JoinHorizontal(lipgloss.Top,
				leftStyle.Render(leftContent),
				rightStyle.Render(rightContent),
			)
		} else {
			content = StylePanel.Width(uiWidth).Render(m.viewList(uiWidth))
		}
	} else if m.mode == ViewSSHForm {
		content = m.viewSSHForm()
	} else if m.mode == ViewSSHSaved {
		content = m.viewSSHSaved()
	}

	// Footer
	footer := ""
	if m.mode == ViewList {
		var activeContainer *models.ContainerData
		if len(m.state.Containers) > 0 && m.cursor >= 0 && m.cursor < len(m.state.Containers) {
			activeContainer = &m.state.Containers[m.cursor]
		}

		actions := " [↑/↓] Navigate "
		if activeContainer != nil {
			// Check if restarting
			isRestarting := false
			for _, id := range activeContainer.GroupIDs {
				if m.restartingIDs[id] {
					isRestarting = true
					break
				}
			}

			if activeContainer.State != "running" {
				actions += " [a] Start "
			}
			if activeContainer.State != "exited" && activeContainer.State != "created" {
				actions += " [x] Stop "
			}
			if isRestarting {
				actions += StyleStatusUp.Render(" [Restarting...] ")
			} else {
				actions += " [r] Restart "
			}
		}

		help := actions + " [Enter] Details  [o] Sort  [i] Invert  [s] SSH  [l] Local  [q] Quit "
		if m.showDetails {
			help = actions + " [Tab] Switch Panel  [Enter] Close Details  [q] Quit "
		}
		footer = StyleHelp.Render(help)
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

func (m *AppModel) viewList(width int) string {
	if len(m.state.Containers) == 0 {
		if !m.firstFetchDone {
			return "Loading containers..."
		}
		return "No containers found."
	}

	// Original widths or adjusted for split
	wName := 25
	wState := 10
	wCPU := 22
	wMem := 22
	wDisk := 18
	wPorts := 20

	// If we are in split mode (width is small), we prioritize Name and State, then CPU/RAM
	headerRow := lipgloss.JoinHorizontal(lipgloss.Left,
		lipgloss.NewStyle().Width(wName).PaddingRight(2).Render("NAME"),
		lipgloss.NewStyle().Width(wState).PaddingRight(2).Render("STATE"),
	)

	showCPU := width > 50
	showMem := width > 75
	showDisk := width > 100
	showPorts := width > 125

	if showCPU {
		headerRow = lipgloss.JoinHorizontal(lipgloss.Left, headerRow, lipgloss.NewStyle().Width(wCPU).PaddingRight(2).Render("CPU %"))
	}
	if showMem {
		headerRow = lipgloss.JoinHorizontal(lipgloss.Left, headerRow, lipgloss.NewStyle().Width(wMem).PaddingRight(2).Render("RAM %"))
	}
	if showDisk {
		headerRow = lipgloss.JoinHorizontal(lipgloss.Left, headerRow, lipgloss.NewStyle().Width(wDisk).PaddingRight(2).Render("DISK I/O"))
	}
	if showPorts {
		headerRow = lipgloss.JoinHorizontal(lipgloss.Left, headerRow, lipgloss.NewStyle().Width(wPorts).Render("PORTS"))
	}

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

		rowContent := lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Width(wName).PaddingRight(2).Render(name),
			stateStyle.Copy().Width(wState).PaddingRight(2).Render(c.State),
		)

		if showCPU {
			cpuStr := fmt.Sprintf("%5.1f%% ", c.CPUPercent) + DrawProgressBar(c.CPUPercent, 10, true)
			rowContent = lipgloss.JoinHorizontal(lipgloss.Left, rowContent, lipgloss.NewStyle().Width(wCPU).PaddingRight(2).Render(cpuStr))
		}
		if showMem {
			memStr := fmt.Sprintf("%5.1f%% ", c.MemPercent) + DrawProgressBar(c.MemPercent, 10, true)
			rowContent = lipgloss.JoinHorizontal(lipgloss.Left, rowContent, lipgloss.NewStyle().Width(wMem).PaddingRight(2).Render(memStr))
		}
		if showDisk {
			rowContent = lipgloss.JoinHorizontal(lipgloss.Left, rowContent, lipgloss.NewStyle().Width(wDisk).PaddingRight(2).Render(c.BlockIO))
		}
		if showPorts {
			rowContent = lipgloss.JoinHorizontal(lipgloss.Left, rowContent, lipgloss.NewStyle().Width(wPorts).Render(c.Ports))
		}

		if i == m.cursor {
			rows = append(rows, StyleActiveRow.Render(rowContent))
		} else {
			rows = append(rows, StyleNormalRow.Render(rowContent))
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m *AppModel) viewDetails(width int) string {
	if len(m.state.Containers) == 0 || m.cursor >= len(m.state.Containers) {
		return "No container selected"
	}

	c := m.state.Containers[m.cursor]

	renderRow := func(label, value string, style lipgloss.Style) string {
		visualWidth := lipgloss.Width(value)
		if visualWidth > width-16 {
			value = runewidth.Truncate(value, width-19, "...")
		}
		return lipgloss.JoinHorizontal(lipgloss.Top,
			StyleDetailLabel.Render(label),
			style.Render(value),
		)
	}

	var sections []string

	// General Info
	image := c.Image
	if strings.HasPrefix(image, "sha256:") {
		image = image[7:19] + " (Hash)"
	}

	stateVal := c.State
	stateStyle := StyleDetailValue
	runningCount := 0
	if len(c.SubContainers) > 0 {
		for _, sub := range c.SubContainers {
			if sub.State == "running" {
				runningCount++
			}
		}
		if c.State == "mixed" {
			stateVal = fmt.Sprintf("Mixed (%d/%d Running)", runningCount, len(c.SubContainers))
			stateStyle = lipgloss.NewStyle().Foreground(ColorWarning)
		} else if c.State == "running" {
			stateStyle = StyleStatusUp
		} else {
			stateStyle = StyleStatusDown
		}
	}

	sections = append(sections, StyleTitle.Render("General Info"))
	sections = append(sections, renderRow("ID", c.ID, StyleDetailValue))
	sections = append(sections, renderRow("Name", c.Name, StyleDetailValue))
	sections = append(sections, renderRow("Image", image, StyleDetailValue))
	sections = append(sections, renderRow("State", stateVal, stateStyle))
	sections = append(sections, renderRow("Status", c.Status, StyleDetailValue))
	sections = append(sections, "")

	// Resource Usage
	resRows := []string{StyleTitle.Render("Resource Usage")}
	
	// Add Health Bar for groups
	if len(c.SubContainers) > 1 {
		healthPct := (float64(runningCount) / float64(len(c.SubContainers))) * 100.0
		healthBar := DrawProgressBar(healthPct, 15, false) // high is NOT bad (high is good)
		resRows = append(resRows, renderRow("Health", healthBar, StyleDetailValue))
	}

	cpu := fmt.Sprintf("%.2f%%", c.CPUPercent)
	mem := fmt.Sprintf("%.2f%%", c.MemPercent)
	if c.MemUsage != "" && c.MemUsage != "0B / 0B" {
		mem += fmt.Sprintf(" (%s)", c.MemUsage)
	}

	resRows = append(resRows, renderRow("CPU %", cpu, StyleDetailValue))
	resRows = append(resRows, renderRow("RAM %", mem, StyleDetailValue))
	
	if c.NetIO != "" && c.NetIO != "↓0B ↑0B" {
		resRows = append(resRows, renderRow("Net I/O", c.NetIO, StyleDetailValue))
	}
	if c.BlockIO != "" && c.BlockIO != "↓0B ↑0B" {
		resRows = append(resRows, renderRow("Disk I/O", c.BlockIO, StyleDetailValue))
	}
	if c.PIDs > 0 {
		resRows = append(resRows, renderRow("PIDs", fmt.Sprintf("%d", c.PIDs), StyleDetailValue))
	}
	sections = append(sections, resRows...)
	sections = append(sections, "")

	// Group Members
	if len(c.SubContainers) > 1 {
		sections = append(sections, StyleTitle.Render("Group Members"))
		for _, sub := range c.SubContainers {
			subStatusStyle := StyleStatusDown
			if sub.State == "running" {
				subStatusStyle = StyleStatusUp
			}
			portInfo := ""
			if sub.Ports != "" {
				portInfo = " :" + sub.Ports
			}
			subName := sub.Name
			if len(subName) > width-25 {
				subName = subName[:width-28] + "..."
			}
			sections = append(sections, fmt.Sprintf("• %s [%s]%s", subName, subStatusStyle.Render(sub.State), portInfo))
		}
		sections = append(sections, "")
	}

	// Network
	if c.Ports != "" {
		sections = append(sections, StyleTitle.Render("Network"))
		sections = append(sections, renderRow("Ports", c.Ports, StyleDetailValue))
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
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
