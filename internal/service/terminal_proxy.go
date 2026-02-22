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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// ansiEscape matches ANSI escape sequences (CSI, OSC, etc.) for stripping from PTY output.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\|\x1b[=_P].*?\x1b\\`)

// bashReader drains a stream into a buffered channel using a single persistent
// goroutine. Prevents goroutine leaks between sequential commands.
type bashReader struct {
	ch  <-chan []byte // receives chunks as they arrive
	buf []byte        // leftover bytes read past the marker of the previous command
}

func startBashReader(ctx context.Context, r io.Reader) *bashReader {
	ch := make(chan []byte, 256)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case ch <- chunk:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				close(ch)
				return
			}
		}
	}()
	return &bashReader{ch: ch}
}

// cleanPTYOutput strips ANSI escapes and keeps only actual command output (PTY echoes the command line).
// Stream order: [prompt] [optional echoed input] [command stdout] [marker line]. We take content before the marker.
func cleanPTYOutput(s, markerID string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = ansiEscape.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	// Find the marker line; keep only lines before it.
	cut := len(lines)
	for i, line := range lines {
		if strings.Contains(line, markerID) {
			cut = i
			break
		}
	}
	lines = lines[:cut]
		// Drop lines that look like echoed command, prompt, or marker artifact (e.g. "0329705__:'$__x", literal \n).
		var out []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" ||
				trimmed == `\n` ||
				strings.Contains(line, "; echo '") ||
				strings.Contains(line, markerID) ||
				strings.Contains(line, ":'$") {
				continue
			}
			out = append(out, line)
		}
		return strings.TrimSpace(strings.Join(out, "\n"))
}

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

	mu                      sync.Mutex
	bashExec                *docker.LongRunningExec
	bashContainer           string
	stdoutReader            *bashReader // session-level stdout reader
	stderrReader            *bashReader // session-level stderr reader
	useTty                  bool        // cached from env, set at session start
	previousCommandTimedOut bool        // set when readUntilMarker times out; cleared before next command
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
				if err == io.EOF || strings.Contains(err.Error(), "EOF") {
					p.logger.Debug("handleConn closed", zap.Error(err))
				} else {
					p.logger.Error("handleConn error", zap.Error(err))
				}
			}
		}(conn)
	}
}

func (p *Proxy) handleConn(ctx context.Context, conn net.Conn) error {
	p.logger.Debug("New connection accepted", zap.String("socket", p.socketPath))
	dec := json.NewDecoder(conn)
	var req request
	if err := dec.Decode(&req); err != nil {
		if err == io.EOF {
			p.logger.Debug("Connection closed without request (e.g. socket probe)", zap.Error(err))
		} else {
			p.logger.Error("Failed to decode request", zap.Error(err))
		}
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

		// Create long-running shell with PTY so "sh -i" works without "can't access tty; job control turned off"
		useTty := os.Getenv("DIND_USE_PTY") != "0" && strings.ToLower(strings.TrimSpace(os.Getenv("DIND_USE_PTY"))) != "false"
		bashExec, err := p.dockerClient.CreateLongRunningExec(ctx, containerID, []string{"sh", "-i"}, useTty)
		if err != nil {
			p.mu.Unlock()
			return p.writeErrorBuf(bufWriter, enc, fmt.Errorf("failed to start bash: %w", err))
		}

		p.bashExec = bashExec
		p.bashContainer = containerID
		p.useTty = useTty
		p.stdoutReader = startBashReader(ctx, bashExec.Stdout)
		p.stderrReader = startBashReader(ctx, bashExec.Stderr)
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

func stripAnsi(s string) string {
	return strings.TrimSpace(ansiEscape.ReplaceAllString(strings.ReplaceAll(s, "\r", ""), ""))
}

func parseExitCode(s string) int {
	s = strings.TrimPrefix(strings.TrimSpace(s), ":")
	code, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return code
}

// cleanCommandOutput strips ANSI codes and (in PTY mode) removes echoed command line.
func cleanCommandOutput(s, markerID string, useTty bool) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = ansiEscape.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if useTty {
		s = cleanPTYOutput(s, markerID)
	}
	return s
}

// readUntilMarker reads from r until markerID is found or timeout expires.
// Returns cleaned output and exit code parsed from marker line ("MARKER:exitcode").
// Any data after the marker line is saved in r.buf for the next command.
func (p *Proxy) readUntilMarker(r *bashReader, markerID string, timeout time.Duration, ctx context.Context) (string, int) {
	var acc strings.Builder
	if len(r.buf) > 0 {
		acc.Write(r.buf)
		r.buf = nil
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		select {
		case chunk, ok := <-r.ch:
			if !ok {
				return stripAnsi(acc.String()), -1
			}
			acc.Write(chunk)
			s := acc.String()
			// With PTY we output "\nMARKER:0" so the real marker line is uniquely "\n" + markerID + ":" + digit (echoed line has no leading \n before marker).
			markerLineStart := "\n" + markerID + ":"
			idx := -1
			for pos := 0; ; {
				i := strings.Index(s[pos:], markerLineStart)
				if i < 0 {
					break
				}
				candidate := pos + i
				if candidate+len(markerLineStart) < len(s) && s[candidate+len(markerLineStart)] >= '0' && s[candidate+len(markerLineStart)] <= '9' {
					idx = candidate
					break
				}
				pos = candidate + 1
			}
			if idx < 0 {
				// No PTY or legacy: match markerID + ":" + digit (e.g. "__CMD_DONE_xxx:0")
				markerWithColon := markerID + ":"
				for pos := 0; ; {
					i := strings.Index(s[pos:], markerWithColon)
					if i < 0 {
						break
					}
					candidate := pos + i
					if candidate+len(markerWithColon) < len(s) && s[candidate+len(markerWithColon)] >= '0' && s[candidate+len(markerWithColon)] <= '9' {
						idx = candidate
						markerLineStart = markerWithColon
						break
					}
					pos = candidate + 1
				}
			}
			if idx >= 0 {
				output := s[:idx]
				rest := s[idx+len(markerLineStart):]
				nl := strings.IndexByte(rest, '\n')
				exitCode := 0
				if nl >= 0 {
					exitCode = parseExitCode(rest[:nl])
					r.buf = []byte(rest[nl+1:])
				} else {
					r.buf = []byte(rest)
				}
				return cleanCommandOutput(output, markerID, p.useTty), exitCode
			}
		case <-ctx.Done():
			return stripAnsi(acc.String()), -1
		case <-time.After(remaining):
			// timeout
		}
		if time.Now().After(deadline) {
			break
		}
	}
	return cleanCommandOutput(acc.String(), markerID, p.useTty), -1
}

// drainReader reads from r until 500ms of inactivity or maxWait expires.
func (p *Proxy) drainReader(r *bashReader, maxWait time.Duration, ctx context.Context) string {
	var acc strings.Builder
	if len(r.buf) > 0 {
		acc.Write(r.buf)
		r.buf = nil
	}
	idle := time.NewTimer(500 * time.Millisecond)
	defer idle.Stop()
	deadline := time.Now().Add(maxWait)
	for {
		select {
		case chunk, ok := <-r.ch:
			if !ok {
				return acc.String()
			}
			acc.Write(chunk)
			idle.Reset(500 * time.Millisecond)
		case <-idle.C:
			return acc.String()
		case <-ctx.Done():
			return acc.String()
		case <-time.After(time.Until(deadline)):
			return acc.String()
		}
	}
}

// executeCommandInBash executes a command in the active bash process
func (p *Proxy) executeCommandInBash(ctx context.Context, bashExec *docker.LongRunningExec, command string, conn net.Conn, bufWriter *bufio.Writer, enc *json.Encoder) error {
	// If previous command timed out, the shell may still be blocked on that foreground process.
	// With PTY we free the console by sending Ctrl+Z (suspend) then "bg" so the job continues in background.
	// Without PTY, Ctrl+Z does not send SIGTSTP; set DIND_USE_PTY=1 to enable freeing the console.
	p.mu.Lock()
	needFreeConsole := p.previousCommandTimedOut && p.useTty
	if p.previousCommandTimedOut && !p.useTty {
		p.logger.Debug("Previous command timed out; console not freed (requires DIND_USE_PTY=1)")
	}
	if p.previousCommandTimedOut {
		p.previousCommandTimedOut = false
	}
	p.mu.Unlock()

	if needFreeConsole {
		if _, err := bashExec.Stdin.Write([]byte("\x1abg\n")); err != nil {
			p.logger.Warn("Failed to send Ctrl+Z and bg to free console", zap.Error(err))
		} else {
			// Drain stdout briefly to clear shell messages (suspend/bg and prompt) before next command output
			_ = p.drainReader(p.stdoutReader, 500*time.Millisecond, ctx)
		}
	}

	markerID := fmt.Sprintf("__CMD_DONE_%d__", time.Now().UnixNano())
	// Capture real exit code in marker line; leading \n so PTY stream has "\nMARKER:0" (never matches echoed command).
	// With PTY, sleep 0.05s so command stdout is flushed before the marker line.
	wrapped := fmt.Sprintf("%s; __x=$?; sleep 0.05 2>/dev/null || true; echo '\\n%s:'$__x\n", command, markerID)

	p.logger.Debug("Writing command to bash", zap.String("command", command), zap.String("markerID", markerID))
	if _, err := bashExec.Stdin.Write([]byte(wrapped)); err != nil {
		p.logger.Error("Failed to write command to bash", zap.Error(err))
		return p.writeErrorBuf(bufWriter, enc, fmt.Errorf("write command: %w", err))
	}

	timeout := time.Duration(p.timeoutSeconds) * time.Second

	stdout, exitCode := p.readUntilMarker(p.stdoutReader, markerID, timeout, ctx)
	if exitCode == -1 {
		p.mu.Lock()
		p.previousCommandTimedOut = true
		p.mu.Unlock()
	}
	stderr := p.drainReader(p.stderrReader, 3*time.Second, ctx)

	_ = p.updateLastActive(time.Now())

	p.logger.Info("Command executed", zap.String("command", command), zap.Int("stdout_len", len(stdout)), zap.Int("stderr_len", len(stderr)), zap.Int("exitCode", exitCode))

	resp := execResponse{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	respBytes = append(respBytes, '\n')

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Write(respBytes)
	conn.SetWriteDeadline(time.Time{})
	return err
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
		Labels:     map[string]string{"ml_component": "proxy"},
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
