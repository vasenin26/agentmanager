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

	mu            sync.Mutex
	bashExec      *docker.LongRunningExec
	bashContainer string
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
	// Use smaller buffer size (512 bytes) to ensure data is sent more frequently
	bufWriter := bufio.NewWriterSize(conn, 512)
	enc := json.NewEncoder(bufWriter)

	var err error
	switch req.Action {
	case "exec":
		if req.Command == "" {
			err = p.writeErrorBuf(bufWriter, enc, errors.New("missing command"))
		} else {
			// For exec action, start bash session and handle multiple commands through same connection
			err = p.handleExecSession(ctx, conn, bufWriter, enc, req.Command)
			// Don't close connection here - it will be closed when session ends
			return err
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

// handleExecSession handles exec action by starting bash and processing multiple commands
func (p *Proxy) handleExecSession(ctx context.Context, conn net.Conn, bufWriter *bufio.Writer, enc *json.Encoder, firstCommand string) error {
	p.mu.Lock()

	// Ensure container exists and is running
	containerID, err := p.ensureContainer(ctx)
	if err != nil {
		p.mu.Unlock()
		return p.writeErrorBuf(bufWriter, enc, err)
	}

	// Start bash process if not already started or if container changed
	if p.bashExec == nil || p.bashContainer != containerID {
		// Close old bash process if exists
		if p.bashExec != nil {
			p.bashExec.Stdin.Close()
			p.bashExec.Stdout.Close()
			p.bashExec.Stderr.Close()
		}

		// Wait for Docker daemon to be ready
		if err := p.waitForDockerDaemon(ctx, containerID); err != nil {
			p.logger.Warn("Docker daemon might not be ready, continuing anyway", zap.Error(err))
		}

		// Create long-running bash process (use sh if bash is not available)
		bashExec, err := p.dockerClient.CreateLongRunningExec(ctx, containerID, []string{"sh", "-i"})
		if err != nil {
			p.mu.Unlock()
			return p.writeErrorBuf(bufWriter, enc, fmt.Errorf("failed to start bash: %w", err))
		}

		p.bashExec = bashExec
		p.bashContainer = containerID
		p.logger.Info("Bash process started", zap.String("containerID", containerID), zap.String("execID", bashExec.ExecID))
	}

	bashExec := p.bashExec
	p.mu.Unlock()

	// Update last active
	_ = p.updateLastActive(time.Now())

	// Send first command
	if firstCommand != "" {
		if err := p.executeCommandInBash(ctx, bashExec, firstCommand, conn, bufWriter, enc); err != nil {
			return err
		}
	}

	// Continue reading commands from connection and executing them
	dec := json.NewDecoder(conn)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				p.logger.Debug("Connection closed by client")
				return nil
			}
			p.logger.Error("Failed to decode request", zap.Error(err))
			return err
		}

		if req.Action != "exec" {
			// For non-exec actions, handle normally but don't close connection
			switch req.Action {
			case "status":
				st, statusErr := p.status(ctx)
				if statusErr != nil {
					_ = p.writeErrorBuf(bufWriter, enc, statusErr)
				} else {
					_ = enc.Encode(st)
				}
				_ = bufWriter.Flush()
			case "destroy":
				if destroyErr := p.destroy(ctx); destroyErr != nil {
					_ = p.writeErrorBuf(bufWriter, enc, destroyErr)
				} else {
					_ = enc.Encode(map[string]string{"result": "ok"})
				}
				_ = bufWriter.Flush()
				return nil
			default:
				_ = p.writeErrorBuf(bufWriter, enc, fmt.Errorf("unknown action: %s", req.Action))
				_ = bufWriter.Flush()
			}
			continue
		}

		if req.Command == "" {
			_ = p.writeErrorBuf(bufWriter, enc, errors.New("missing command"))
			_ = bufWriter.Flush()
			continue
		}

		if err := p.executeCommandInBash(ctx, bashExec, req.Command, conn, bufWriter, enc); err != nil {
			return err
		}
	}
}

// executeCommandInBash executes a command in the active bash process
func (p *Proxy) executeCommandInBash(ctx context.Context, bashExec *docker.LongRunningExec, command string, conn net.Conn, bufWriter *bufio.Writer, enc *json.Encoder) error {
	// Generate unique marker for command completion detection
	markerID := fmt.Sprintf("__CMD_DONE_%d__", time.Now().UnixNano())

	// Wrap command with marker to detect completion
	// Use command && echo marker to ensure marker only appears if command succeeds
	// For commands that might fail, we still want to see the marker
	wrappedCommand := fmt.Sprintf("%s; echo '%s'\n", command, markerID)


	// Write wrapped command to shell stdin
	p.logger.Debug("Writing command to bash", zap.String("command", command), zap.String("markerID", markerID))
	_, err := bashExec.Stdin.Write([]byte(wrappedCommand))
	if err != nil {
		p.logger.Error("Failed to write command to bash", zap.Error(err))
		return p.writeErrorBuf(bufWriter, enc, fmt.Errorf("failed to write command: %w", err))
	}
	p.logger.Debug("Command written to bash, waiting for output", zap.String("command", command))

	// Read response from bash stdout and stderr
	// We need to read until we get a prompt or timeout
	// For simplicity, we'll read with a timeout and collect output
	timeout := 60 * time.Second
	if p.timeoutSeconds > 0 {
		timeout = time.Duration(p.timeoutSeconds) * time.Second
	}

	stdoutChan := make(chan string, 1)
	stderrChan := make(chan string, 1)
	errChan := make(chan error, 2)

	// Read stdout with timeout and marker detection
	go func() {
		buf := make([]byte, 4096)
		var output strings.Builder
		deadline := time.Now().Add(timeout)
		markerFound := false

		for time.Now().Before(deadline) && !markerFound {
			// Use select with timeout for non-blocking read
			readDone := make(chan bool, 1)
			var n int
			var readErr error

			go func() {
				n, readErr = bashExec.Stdout.Read(buf)
				readDone <- true
			}()

			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			case <-readDone:
				if n > 0 {
					output.Write(buf[:n])
					// Check if marker is in the output
					outputStr := output.String()
					if strings.Contains(outputStr, markerID) {
						markerFound = true
						// Remove marker and everything after it from output
						markerIdx := strings.Index(outputStr, markerID)
						if markerIdx > 0 {
							// Also remove the newline before marker if present
							cleanOutput := outputStr[:markerIdx]
							cleanOutput = strings.TrimSuffix(cleanOutput, "\n")
							cleanOutput = strings.TrimSuffix(cleanOutput, "\r")
							p.logger.Debug("Marker found in stdout", zap.String("command", command), zap.String("outputLength", fmt.Sprintf("%d", len(cleanOutput))))
							stdoutChan <- cleanOutput
						} else {
							p.logger.Debug("Marker found but at start of output", zap.String("command", command))
							stdoutChan <- ""
						}
						return
					}
				}
				if readErr != nil {
				if readErr == io.EOF {
					// EOF before marker - command might have failed or shell closed
					outputStr := output.String()
					// Remove marker if somehow present
					if strings.Contains(outputStr, markerID) {
						markerIdx := strings.Index(outputStr, markerID)
						if markerIdx > 0 {
							outputStr = outputStr[:markerIdx]
							outputStr = strings.TrimSuffix(outputStr, "\n")
							outputStr = strings.TrimSuffix(outputStr, "\r")
						}
					}
					p.logger.Warn("EOF received before marker found", zap.String("command", command), zap.String("outputLength", fmt.Sprintf("%d", len(outputStr))))
					stdoutChan <- outputStr
					return
				}
					errChan <- readErr
					return
				}
			case <-time.After(500 * time.Millisecond):
				// Check if we should continue or timeout
				if time.Now().After(deadline) {
					// Timeout - return what we have, removing marker if present
					outputStr := output.String()
					if strings.Contains(outputStr, markerID) {
						markerIdx := strings.Index(outputStr, markerID)
						if markerIdx > 0 {
							outputStr = outputStr[:markerIdx]
							outputStr = strings.TrimSuffix(outputStr, "\n")
							outputStr = strings.TrimSuffix(outputStr, "\r")
						}
					}
					p.logger.Warn("Timeout reading stdout, returning partial output", zap.String("command", command), zap.String("outputLength", fmt.Sprintf("%d", len(outputStr))))
					stdoutChan <- outputStr
					return
				}
			}
		}

		// If we exit loop without finding marker, return what we have
		if !markerFound {
			outputStr := output.String()
			if strings.Contains(outputStr, markerID) {
				markerIdx := strings.Index(outputStr, markerID)
				if markerIdx > 0 {
					outputStr = outputStr[:markerIdx]
					outputStr = strings.TrimSuffix(outputStr, "\n")
					outputStr = strings.TrimSuffix(outputStr, "\r")
				}
			}
			p.logger.Warn("Marker not found in stdout, returning partial output", zap.String("command", command), zap.String("outputLength", fmt.Sprintf("%d", len(outputStr))))
			stdoutChan <- outputStr
		}
	}()

	// Read stderr with timeout (marker won't be in stderr, but we still need to read it)
	go func() {
		buf := make([]byte, 4096)
		var output strings.Builder
		deadline := time.Now().Add(timeout)
		lastReadTime := time.Now()

		for time.Now().Before(deadline) {
			readDone := make(chan bool, 1)
			var n int
			var readErr error

			go func() {
				n, readErr = bashExec.Stderr.Read(buf)
				readDone <- true
			}()

			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			case <-readDone:
				if n > 0 {
					output.Write(buf[:n])
					lastReadTime = time.Now()
				}
				if readErr != nil {
					if readErr == io.EOF {
						stderrChan <- output.String()
						return
					}
					errChan <- readErr
					return
				}
			case <-time.After(500 * time.Millisecond):
				if time.Now().After(deadline) {
					stderrChan <- output.String()
					return
				}
				// If we haven't read anything for a while and stdout found marker, we're done
				if output.Len() > 0 && time.Since(lastReadTime) > 1*time.Second {
					// Give a bit more time for stderr to complete
					time.Sleep(300 * time.Millisecond)
					stderrChan <- output.String()
					return
				}
			}
		}
		stderrChan <- output.String()
	}()

	// Wait for output or timeout
	var stdout, stderr string
	select {
	case <-ctx.Done():
		p.logger.Warn("Context cancelled while waiting for command output", zap.String("command", command))
		return p.writeErrorBuf(bufWriter, enc, ctx.Err())
	case stdout = <-stdoutChan:
		// Got stdout, marker was found
		p.logger.Debug("Received stdout from command", zap.String("command", command), zap.String("stdoutLength", fmt.Sprintf("%d", len(stdout))))
	case err := <-errChan:
		p.logger.Error("Error reading stdout", zap.String("command", command), zap.Error(err))
		return p.writeErrorBuf(bufWriter, enc, fmt.Errorf("failed to read stdout: %w", err))
	case <-time.After(timeout):
		// Timeout occurred - check if we got any output from goroutines
		p.logger.Warn("Timeout waiting for command output", zap.String("command", command), zap.Duration("timeout", timeout))
		select {
		case stdout = <-stdoutChan:
			p.logger.Debug("Received stdout after timeout", zap.String("command", command), zap.String("stdoutLength", fmt.Sprintf("%d", len(stdout))))
		default:
			// No output received, command might have completed without output
			// This can happen for commands like 'cd' that don't produce output
			p.logger.Debug("No stdout received, command may have no output", zap.String("command", command))
			stdout = ""
		}
	}

	// Wait for stderr with shorter timeout since stdout is done
	select {
	case stderr = <-stderrChan:
	case err := <-errChan:
		// stderr read error is not critical
		p.logger.Warn("Failed to read stderr", zap.Error(err))
	case <-time.After(2 * time.Second):
		// Give stderr a bit more time, but not too long
		select {
		case stderr = <-stderrChan:
		default:
			stderr = ""
		}
	}

	// Try to determine exit code by checking if command succeeded
	// This is a simplification - in real bash we'd need to check $?
	exitCode := 0
	if strings.Contains(stderr, "error") || strings.Contains(stderr, "Error") {
		exitCode = 1
	}

	// Update last active
	_ = p.updateLastActive(time.Now())

	// Send response
	p.logger.Debug("Preparing to send response", zap.String("command", command), zap.String("stdoutLength", fmt.Sprintf("%d", len(stdout))), zap.String("stderrLength", fmt.Sprintf("%d", len(stderr))), zap.Int("exitCode", exitCode))
	resp := execResponse{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	
	// Serialize response to JSON bytes
	respBytes, err := json.Marshal(resp)
	if err != nil {
		p.logger.Error("Failed to marshal response", zap.String("command", command), zap.Error(err))
		return fmt.Errorf("failed to marshal response: %w", err)
	}
	// Add newline to match json.Encoder behavior
	respBytes = append(respBytes, '\n')
	
	// Write response directly to connection using conn.Write()
	// This bypasses all buffers and writes directly to the socket
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Write(respBytes)
	conn.SetWriteDeadline(time.Time{}) // Clear deadline
	
	if err != nil {
		p.logger.Error("Failed to write response to connection", zap.String("command", command), zap.Error(err), zap.Int("bytesWritten", n), zap.Int("totalBytes", len(respBytes)))
		return fmt.Errorf("failed to write response: %w", err)
	}
	
	if n != len(respBytes) {
		p.logger.Warn("Partial write to connection", zap.String("command", command), zap.Int("bytesWritten", n), zap.Int("totalBytes", len(respBytes)))
	}
	
	p.logger.Info("Command executed and response sent successfully", zap.String("command", command), zap.String("stdoutLength", fmt.Sprintf("%d", len(stdout))), zap.Int("exitCode", exitCode), zap.Int("bytesWritten", n))

	return nil
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
