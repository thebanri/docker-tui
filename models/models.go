package models

import (
	"time"
)

// ContainerData holds the information about a single Docker container.
type ContainerData struct {
	ID             string
	GroupIDs       []string
	SubContainers  []ContainerData // For grouped containers
	Name           string
	Project        string
	Image      string
	State      string // "running", "exited", etc.
	Status     string // "Up 2 hours", etc.
	Ports      string
	CPUPercent float64
	MemPercent float64
	MemUsage   string // e.g. "50MiB / 1GiB"
	NetIO      string // e.g. "10kB / 5kB"
	BlockIO    string // e.g. "1MB / 5MB" (Used as Disk usage proxy)
	PIDs       uint64
	Updated    time.Time
}

// ConnectionType defines the type of Docker connection
type ConnectionType string

const (
	LocalConnection ConnectionType = "Local"
	SSHConnection   ConnectionType = "SSH"
)

// AppState represents the global state of the TUI
type AppState struct {
	Containers     []ContainerData
	ConnectionType ConnectionType
	ServerName     string // "" for local, "user@ip" for SSH
	Error          error
}
