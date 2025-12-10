package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// Proxy provides a command proxy that listens on a Unix socket for JSON requests
// and executes commands inside a DinD container. It manages container lifecycle
// (start/stop/remove) and persists last activity metadata.
type Proxy struct {
	id              string
	socketPath      string
	metaPath        string
	dockerClient    docker.DockerClient
	image           string
	registry        docker.AuthConfig
	timeoutSeconds  int
	idleTimeout     time.Duration
	removalDuration time.Duration
	hostSharedPath  string
	logger          *zap.Logger

	mu sync.Mutex
}

type proxyMeta struct {
	ContainerID  string    `json:"container_id"`
	LastActiveAt time.Time `json:"last_active_at"`
}

type request struct {
	Action  string `json:"action"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // Timeout in seconds (0 = use default)
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

type statusResponse struct {
	ContainerID  string `json:"container_id,omitempty"`
	State        string `json:"state,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// NewProxy creates a new Proxy for a given logical ID. The socket will be created at /var/run/orchestrator/{id}.sock
// image - DinD image to use; hostSharedPath - host path to bind into container at /shared-data
// NewProxy creates a new Proxy for a given logical ID. The socket will be created at /var/run/orchestrator/{id}.sock
// image - DinD image to use; hostVolumeID - docker volume name or host path to bind into container at /shared-data
func NewProxy(id string, dc docker.DockerClient, image string, hostVolumeID string, registry docker.AuthConfig, logger *zap.Logger) *Proxy {
	// Use configurable socket directory, default to /tmp/orchestrator for better Docker Desktop WSL2 compatibility
	socketDir := os.Getenv("ORCHESTRATOR_SOCKET_DIR")
	if socketDir == "" {
		socketDir = "/tmp/orchestrator"
	}
	socketPath := filepath.Join(socketDir, id+".sock")
	metaDir := filepath.Join("data", "command_proxy")
	_ = os.MkdirAll(metaDir, 0o755)
	metaPath := filepath.Join(metaDir, id+".json")

	return &Proxy{
		id:              id,
		socketPath:      socketPath,
		metaPath:        metaPath,
		dockerClient:    dc,
		image:           image,
		registry:        registry,
		timeoutSeconds:  60,
		idleTimeout:     5 * time.Minute,
		removalDuration: 7 * 24 * time.Hour,
		hostSharedPath:  hostVolumeID,
		logger:          logger,
	}
}

// Serve starts listening on the Unix socket and handles incoming requests.
func (p *Proxy) Serve(ctx context.Context) error {
	// ensure dir exists
	if err := os.MkdirAll(filepath.Dir(p.socketPath), 0o755); err != nil {
		return fmt.Errorf("failed to create socket dir: %w", err)
	}

	// remove stale socket
	if _, err := os.Stat(p.socketPath); err == nil {
		_ = os.Remove(p.socketPath)
	}

	l, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix socket: %w", err)
	}
	defer l.Close()
	_ = os.Chmod(p.socketPath, 0o660)

	// start background lifecycle manager
	go p.lifecycleManager(ctx)

	p.logger.Info("Command proxy listening", zap.String("socket", p.socketPath))

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				p.logger.Error("accept error", zap.Error(err))
				continue
			}
		}
		go func(c net.Conn) {
			defer func() {
				// Small delay to ensure data is sent before closing
				time.Sleep(10 * time.Millisecond)
				c.Close()
			}()
			if err := p.handleConn(ctx, c); err != nil {
				p.logger.Error("handleConn error", zap.Error(err))
			}
		}(conn)
	}
}

func (p *Proxy) handleConn(ctx context.Context, conn net.Conn) error {
	p.logger.Debug("New connection accepted", zap.String("socket", p.socketPath))
	dec := json.NewDecoder(conn)
	var req request
	if err := dec.Decode(&req); err != nil {
		p.logger.Error("Failed to decode request", zap.Error(err))
		return fmt.Errorf("decode request: %w", err)
	}
	p.logger.Debug("Request decoded", zap.String("action", req.Action), zap.String("command", req.Command))

	// Use buffered writer to ensure data is flushed before connection closes
	bufWriter := bufio.NewWriter(conn)
	enc := json.NewEncoder(bufWriter)

	var err error
	switch req.Action {
	case "exec":
		if req.Command == "" {
			err = p.writeErrorBuf(bufWriter, enc, errors.New("missing command"))
		} else {
			// Use custom timeout if provided, otherwise use default
			timeout := req.Timeout
			if timeout <= 0 {
				timeout = p.timeoutSeconds
			}
			// Limit maximum timeout to prevent abuse (e.g., max 10 minutes)
			if timeout > 600 {
				timeout = 600
			}
			p.logger.Debug("Executing command", zap.String("command", req.Command), zap.Int("timeout", timeout))
			stdout, stderr, exitCode, execErr := p.execWithTimeout(ctx, req.Command, timeout)
			if execErr != nil {
				p.logger.Error("exec failed", zap.Error(execErr))
			}
			resp := execResponse{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
			p.logger.Debug("Preparing response", zap.Int("exitCode", exitCode), zap.Int("stdoutLen", len(stdout)), zap.Int("stderrLen", len(stderr)))
			err = enc.Encode(resp)
			if err != nil {
				p.logger.Error("Failed to encode response", zap.Error(err))
			}
		}
	case "status":
		st, statusErr := p.status(ctx)
		if statusErr != nil {
			err = p.writeErrorBuf(bufWriter, enc, statusErr)
		} else {
			err = enc.Encode(st)
		}
	case "destroy":
		if destroyErr := p.destroy(ctx); destroyErr != nil {
			err = p.writeErrorBuf(bufWriter, enc, destroyErr)
		} else {
			err = enc.Encode(map[string]string{"result": "ok"})
		}
	default:
		err = p.writeErrorBuf(bufWriter, enc, fmt.Errorf("unknown action: %s", req.Action))
	}

	// Flush buffered data before returning (connection will be closed by defer)
	if err == nil {
		err = bufWriter.Flush()
		if err != nil {
			p.logger.Error("Failed to flush response", zap.Error(err))
		} else {
			p.logger.Info("Response sent successfully", zap.String("action", req.Action))
		}
	} else {
		p.logger.Error("Failed to send response", zap.Error(err), zap.String("action", req.Action))
		// Try to send error response anyway
		if encErr := p.writeErrorBuf(bufWriter, enc, err); encErr == nil {
			_ = bufWriter.Flush()
		}
	}
	return err
}

func (p *Proxy) writeError(w io.Writer, err error) error {
	p.logger.Error("request error", zap.Error(err))
	return json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (p *Proxy) writeErrorBuf(w *bufio.Writer, enc *json.Encoder, err error) error {
	p.logger.Error("request error", zap.Error(err))
	return enc.Encode(map[string]string{"error": err.Error()})
}

func (p *Proxy) exec(ctx context.Context, command string) (string, string, int, error) {
	return p.execWithTimeout(ctx, command, p.timeoutSeconds)
}

func (p *Proxy) execWithTimeout(ctx context.Context, command string, timeoutSeconds int) (string, string, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// ensure container exists and is running
	containerID, err := p.ensureContainer(ctx)
	if err != nil {
		return "", "", -1, err
	}

	// Wait for Docker daemon to be ready inside DinD container
	if err := p.waitForDockerDaemon(ctx, containerID); err != nil {
		p.logger.Warn("Docker daemon might not be ready, continuing anyway", zap.Error(err))
	}

	// update last active
	_ = p.updateLastActive(time.Now())

	// Wrap command to prevent interactive input and ensure it doesn't hang
	// Redirect stdin from /dev/null to prevent commands from waiting for input
	// Use timeout command if available, otherwise just redirect stdin
	wrappedCommand := fmt.Sprintf("%s </dev/null", command)

	stdout, stderr, exitCode, err := p.dockerClient.ExecInContainer(ctx, containerID, []string{"sh", "-lc", wrappedCommand}, timeoutSeconds)
	_ = p.updateLastActive(time.Now())

	// Check if timeout occurred
	if err != nil && strings.Contains(err.Error(), "timeout") {
		p.logger.Warn("Command execution timed out", zap.String("command", command), zap.Int("timeout", timeoutSeconds))
		return stdout, stderr + "\n[Command timed out after " + fmt.Sprintf("%d", timeoutSeconds) + " seconds]", 124, err
	}

	return stdout, stderr, exitCode, err
}

func (p *Proxy) status(ctx context.Context) (*statusResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	meta, err := p.readMeta()
	if err != nil {
		if os.IsNotExist(err) {
			return &statusResponse{}, nil
		}
		return nil, err
	}

	var state string
	if meta.ContainerID != "" {
		insp, err := p.dockerClient.InspectContainer(ctx, meta.ContainerID)
		if err == nil {
			state = insp.State
		} else {
			state = "not_found"
		}
	}

	return &statusResponse{ContainerID: meta.ContainerID, State: state, LastActiveAt: meta.LastActiveAt.Format(time.RFC3339)}, nil
}

func (p *Proxy) destroy(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	meta, err := p.readMeta()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if meta.ContainerID != "" {
		_ = p.dockerClient.StopContainer(ctx, meta.ContainerID)
		_ = p.dockerClient.RemoveContainer(ctx, meta.ContainerID)
	}
	_ = os.Remove(p.metaPath)
	return nil
}

// ensureContainer returns containerID, creating it if necessary and starting it if stopped
func (p *Proxy) ensureContainer(ctx context.Context) (string, error) {
	meta, err := p.readMeta()
	if err != nil {
		if os.IsNotExist(err) {
			// Meta file doesn't exist yet, create empty meta
			meta = &proxyMeta{}
		} else {
			return "", err
		}
	}

	if meta != nil && meta.ContainerID != "" {
		// inspect
		if _, err := p.dockerClient.InspectContainer(ctx, meta.ContainerID); err == nil {
			// try to start (safe even if running)
			_ = p.dockerClient.StartContainer(ctx, meta.ContainerID)
			return meta.ContainerID, nil
		}
		// otherwise, create new
	}

	p.logger.Info("Creating new DinD container", zap.String("image", p.image), zap.String("volume", p.hostSharedPath))

	// Pull image if not exists
	if err := p.dockerClient.PullImage(ctx, p.image, p.registry); err != nil {
		p.logger.Warn("Failed to pull image, continuing anyway (image might exist locally)", zap.String("image", p.image), zap.Error(err))
	} else {
		p.logger.Info("Image pulled successfully", zap.String("image", p.image))
	}

	cfg := docker.ContainerConfig{
		Image: p.image,
		Env:   map[string]string{},
		Volumes: []docker.VolumeMount{
			{VolumeID: p.hostSharedPath, MountPath: "/opt/repos"},
		},
		AutoRemove: false,
		Privileged: true, // DinD requires privileged mode
	}

	containerID, err := p.dockerClient.CreateContainer(ctx, cfg)
	if err != nil {
		p.logger.Error("Failed to create container", zap.Error(err))
		return "", fmt.Errorf("create container: %w", err)
	}
	p.logger.Info("Container created", zap.String("containerID", containerID))

	if err := p.dockerClient.StartContainer(ctx, containerID); err != nil {
		p.logger.Error("Failed to start container", zap.String("containerID", containerID), zap.Error(err))
		return "", fmt.Errorf("start container: %w", err)
	}
	p.logger.Info("Container started, waiting for Docker daemon to be ready", zap.String("containerID", containerID))

	// Wait for Docker daemon to start inside DinD container
	if err := p.waitForDockerDaemon(ctx, containerID); err != nil {
		p.logger.Warn("Docker daemon might not be ready yet", zap.String("containerID", containerID), zap.Error(err))
	} else {
		p.logger.Info("Docker daemon is ready", zap.String("containerID", containerID))
	}

	meta = &proxyMeta{ContainerID: containerID, LastActiveAt: time.Now()}
	if err := p.writeMeta(meta); err != nil {
		p.logger.Error("failed to write meta", zap.Error(err))
	} else {
		p.logger.Info("Container meta saved", zap.String("containerID", containerID))
	}

	return containerID, nil
}

// waitForDockerDaemon waits for Docker daemon to be ready inside DinD container
func (p *Proxy) waitForDockerDaemon(ctx context.Context, containerID string) error {
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Try to run docker version command to check if daemon is ready
			stdout, stderr, exitCode, err := p.dockerClient.ExecInContainer(ctx, containerID, []string{"sh", "-c", "docker version >/dev/null 2>&1"}, 5)
			if err == nil && exitCode == 0 {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for Docker daemon: stdout=%s, stderr=%s, exitCode=%d", stdout, stderr, exitCode)
			}
		}
	}
}

func (p *Proxy) readMeta() (*proxyMeta, error) {
	b, err := ioutil.ReadFile(p.metaPath)
	if err != nil {
		return nil, err
	}
	var m proxyMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (p *Proxy) writeMeta(m *proxyMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(p.metaPath, b, 0o644)
}

func (p *Proxy) updateLastActive(t time.Time) error {
	meta, err := p.readMeta()
	if err != nil {
		if os.IsNotExist(err) {
			meta = &proxyMeta{}
		} else {
			return err
		}
	}
	meta.LastActiveAt = t
	return p.writeMeta(meta)
}

// lifecycleManager periodically stops idle containers and removes old ones
func (p *Proxy) lifecycleManager(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			meta, err := p.readMeta()
			if err != nil {
				p.mu.Unlock()
				continue
			}

			idleCutoff := time.Now().Add(-p.idleTimeout)
			removeCutoff := time.Now().Add(-p.removalDuration)

			if !meta.LastActiveAt.IsZero() && meta.LastActiveAt.Before(removeCutoff) {
				p.logger.Info("removing container due to inactivity", zap.String("container", meta.ContainerID))
				_ = p.dockerClient.StopContainer(ctx, meta.ContainerID)
				_ = p.dockerClient.RemoveContainer(ctx, meta.ContainerID)
				_ = os.Remove(p.metaPath)
				p.mu.Unlock()
				continue
			}

			if !meta.LastActiveAt.IsZero() && meta.LastActiveAt.Before(idleCutoff) {
				// stop container but keep meta for future start
				p.logger.Info("stopping idle container", zap.String("container", meta.ContainerID))
				_ = p.dockerClient.StopContainer(ctx, meta.ContainerID)
			}
			p.mu.Unlock()
		}
	}
}
