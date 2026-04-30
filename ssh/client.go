package ssh

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"Tuidock/docker"

	dockerclient "github.com/docker/docker/client"
	"golang.org/x/crypto/ssh"
)

type sshWrapper struct {
	docker.Service
	sshClient *ssh.Client
}

func (w *sshWrapper) Close() error {
	w.Service.Close()
	return w.sshClient.Close()
}

// NewRemoteDockerService creates a Docker service connected via SSH
func NewRemoteDockerService(host, port, user, password, privateKey string) (docker.Service, error) {
	var authMethods []ssh.AuthMethod

	if privateKey != "" {
		var keyBytes []byte
		// First try reading as a file path
		b, err := os.ReadFile(privateKey)
		if err == nil {
			keyBytes = b
		} else {
			// Try as raw content (replacing literal \n with actual newlines in case it was pasted as a single line)
			keyContent := strings.ReplaceAll(privateKey, "\\n", "\n")
			keyBytes = []byte(keyContent)
		}

		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			// Fallback: Check if it requires password/passphrase
			return nil, fmt.Errorf("failed to parse private key (path or content invalid/encrypted): %v", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if password != "" {
		authMethods = append(authMethods, ssh.Password(password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication methods provided (need password or private key)")
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "ssh: handshake failed: ssh: unable to authenticate") {
			return nil, fmt.Errorf("AUTH FAILED: Password or private key is incorrect")
		}
		if strings.Contains(errStr, "connection refused") {
			return nil, fmt.Errorf("NETWORK ERROR: Connection refused. Is the server IP/Port correct?")
		}
		if strings.Contains(errStr, "i/o timeout") {
			return nil, fmt.Errorf("NETWORK ERROR: Connection timed out. Check your firewall or VPN")
		}
		if strings.Contains(errStr, "no route to host") {
			return nil, fmt.Errorf("NETWORK ERROR: No route to host. Is the server online?")
		}
		return nil, fmt.Errorf("SSH ERROR: %v", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := sshClient.Dial("unix", "/var/run/docker.sock")
				if err != nil {
					return nil, fmt.Errorf("Docker is not installed, not running, or user lacks permissions on remote host")
				}
				return conn, nil
			},
		},
	}

	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHTTPClient(httpClient),
		dockerclient.WithHost("http://localhost"),
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		sshClient.Close()
		return nil, fmt.Errorf("docker client error: %w", err)
	}

	return &sshWrapper{
		Service:   docker.NewServiceFromClient(cli),
		sshClient: sshClient,
	}, nil
}
