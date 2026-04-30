package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"docker-tui/models"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Service interface for fetching docker data
type Service interface {
	GetContainers() ([]models.ContainerData, error)
	StartContainers(ctx context.Context, ids []string) error
	StopContainers(ctx context.Context, ids []string) error
	RestartContainers(ctx context.Context, ids []string) error
	Close() error
}

type LocalDockerService struct {
	cli *client.Client
}

func NewServiceFromClient(cli *client.Client) Service {
	return &LocalDockerService{cli: cli}
}

func NewLocalDockerService() (Service, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &LocalDockerService{cli: cli}, nil
}

func (s *LocalDockerService) Close() error {
	return s.cli.Close()
}

func (s *LocalDockerService) StartContainers(ctx context.Context, ids []string) error {
	var errs []string
	for _, id := range ids {
		if err := s.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("start errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *LocalDockerService) StopContainers(ctx context.Context, ids []string) error {
	var errs []string
	for _, id := range ids {
		if err := s.cli.ContainerStop(ctx, id, container.StopOptions{}); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("stop errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *LocalDockerService) RestartContainers(ctx context.Context, ids []string) error {
	var errs []string
	for _, id := range ids {
		if err := s.cli.ContainerRestart(ctx, id, container.StopOptions{}); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("restart errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *LocalDockerService) GetContainers() ([]models.ContainerData, error) {
	ctx := context.Background()
	containers, err := s.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "Cannot connect to the Docker daemon") || strings.Contains(errStr, "connection refused") {
			return nil, fmt.Errorf("Docker is not running locally")
		}
		if strings.Contains(errStr, "Docker is not installed, not running, or user lacks permissions on remote host") {
			return nil, fmt.Errorf("Docker is not installed, not running, or user lacks permissions on remote host")
		}
		return nil, err
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	result := make([]models.ContainerData, 0, len(containers))

	for _, c := range containers {
		wg.Add(1)
		go func(c types.Container) {
			defer wg.Done()
			project := c.Labels["com.docker.compose.project"]
			if project == "" {
				nameWithoutSlash := strings.TrimPrefix(c.Names[0], "/")
				idx1 := strings.Index(nameWithoutSlash, "_")
				idx2 := strings.Index(nameWithoutSlash, "-")
				idx := -1
				if idx1 != -1 && (idx2 == -1 || idx1 < idx2) {
					idx = idx1
				} else if idx2 != -1 {
					idx = idx2
				}
				if idx != -1 {
					project = nameWithoutSlash[:idx]
				} else {
					project = nameWithoutSlash
				}
			}

			data := models.ContainerData{
				ID:      c.ID[:12],
				Name:    strings.TrimPrefix(c.Names[0], "/"),
				Project: project,
				Image:   c.Image,
				State:   c.State,
				Status:  c.Status,
				Updated: time.Now(),
			}

			// Format ports
			var ports []string
			for _, p := range c.Ports {
				if p.PublicPort != 0 {
					ports = append(ports, fmt.Sprintf("%d:%d", p.PublicPort, p.PrivatePort))
				} else {
					ports = append(ports, fmt.Sprintf("%d", p.PrivatePort))
				}
			}
			if len(ports) > 3 {
				data.Ports = strings.Join(ports[:3], ", ") + "..."
			} else {
				data.Ports = strings.Join(ports, ", ")
			}

			// Fetch stats only if running
			if c.State == "running" {
				stats, err := s.getStats(ctx, c.ID)
				if err == nil {
					data.CPUPercent = stats.CPUPercent
					data.MemPercent = stats.MemPercent
					data.MemUsage = stats.MemUsage
					data.NetIO = stats.NetIO
					data.BlockIO = stats.BlockIO
					data.PIDs = stats.PIDs
				}
			}

			mu.Lock()
			result = append(result, data)
			mu.Unlock()
		}(c)
	}

	wg.Wait()
	return result, nil
}

// Helper struct to unmarshal docker stats JSON
type v2Stats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IoServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type parsedStats struct {
	CPUPercent float64
	MemPercent float64
	MemUsage   string
	NetIO      string
	BlockIO    string
	PIDs       uint64
}

func (s *LocalDockerService) getStats(ctx context.Context, id string) (parsedStats, error) {
	res := parsedStats{}
	statsResp, err := s.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return res, err
	}
	defer statsResp.Body.Close()

	var stats v2Stats
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		return res, err
	}

	// Calculate CPU
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)
	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = 1
	}
	if systemDelta > 0.0 && cpuDelta > 0.0 {
		res.CPUPercent = (cpuDelta / systemDelta) * 100.0
	}

	// Calculate Memory
	// Memory usage = usage - cache (approximated for linux)
	cache := uint64(0)
	if v, ok := stats.MemoryStats.Stats["inactive_file"]; ok {
		cache = v
	} else if v, ok := stats.MemoryStats.Stats["cache"]; ok {
		cache = v
	}
	usedMem := stats.MemoryStats.Usage - cache
	if stats.MemoryStats.Limit > 0 {
		res.MemPercent = float64(usedMem) / float64(stats.MemoryStats.Limit) * 100.0
	}
	res.MemUsage = fmt.Sprintf("%s / %s", formatBytes(usedMem), formatBytes(stats.MemoryStats.Limit))

	// Calculate Network IO
	var rx, tx uint64
	for _, n := range stats.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	res.NetIO = fmt.Sprintf("%s / %s", formatBytes(rx), formatBytes(tx))

	// Calculate Block IO
	var read, write uint64
	for _, io := range stats.BlkioStats.IoServiceBytesRecursive {
		if strings.ToLower(io.Op) == "read" {
			read += io.Value
		} else if strings.ToLower(io.Op) == "write" {
			write += io.Value
		}
	}
	res.BlockIO = fmt.Sprintf("%s / %s", formatBytes(read), formatBytes(write))

	res.PIDs = stats.PidsStats.Current

	return res, nil
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
