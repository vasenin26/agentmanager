package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// Proxy implements the Command Proxy protocol (agentmodule/docs/command-proxy-protocol.md):
// it listens on a Unix socket for JSON requests and executes commands inside a DinD container.
// It manages container lifecycle (start/stop/remove) and persists last activity metadata.
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

	mu     sync.Mutex // guards the container lifecycle
	metaMu sync.Mutex // guards read-modify-write of the meta file

	jobsMu  sync.Mutex
	jobs    map[string]*job
	maxJobs int
	jobsCtx context.Context // parent of all job contexts, cancelled when Serve stops
	execs   atomic.Int32    // synchronous execs in progress
}

type proxyMeta struct {
	ContainerID  string    `json:"container_id"`
	LastActiveAt time.Time `json:"last_active_at"`
}

const (
	execMaxTimeout     = 600 // seconds
	requestReadTimeout = 10 * time.Second
	responseTimeout    = 10 * time.Second
)

// Error codes of the protocol
const (
	errInvalidAction = "invalid_action"
	errInvalidParams = "invalid_params"
	errJobNotFound   = "job_not_found"
	errProxyBusy     = "proxy_busy"
)

type execRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // Timeout in seconds (0 = use default)
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

type startRequest struct {
	Command     *string           `json:"command"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	MaxLifetime *int              `json:"max_lifetime"`
}

type pollRequest struct {
	JobID        string `json:"job_id"`
	Timeout      *int   `json:"timeout"`
	StdoutOffset int64  `json:"stdout_offset"`
	StderrOffset int64  `json:"stderr_offset"`
}

type killRequest struct {
	JobID  string `json:"job_id"`
	Signal string `json:"signal"`
}

type startResponse struct {
	JobID string `json:"job_id"`
}

type killResponse struct {
	JobID    string `json:"job_id"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code"`
}

type protocolError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string { return e.Code + ": " + e.Message }

func newProtocolError(code, format string, args ...any) *protocolError {
	return &protocolError{Code: code, Message: fmt.Sprintf(format, args...)}
}

type statusResponse struct {
	ContainerID  string `json:"container_id,omitempty"`
	State        string `json:"state,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// NewProxy creates a new Proxy for a given logical ID. The socket will be created at {ORCHESTRATOR_SOCKET_DIR}/{id}.sock
// image - DinD image to use; hostVolumeID - docker volume name or host path to bind into container at /opt/repos
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
		jobs:            map[string]*job{},
		maxJobs:         jobMaxConcurrent,
		jobsCtx:         context.Background(),
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

	p.jobsMu.Lock()
	p.jobsCtx = ctx
	p.jobsMu.Unlock()

	// unblock Accept on shutdown
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

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
			defer c.Close()
			p.handleConn(ctx, c)
		}(conn)
	}
}

// handleConn serves exactly one request: reads one JSON document (a trailing delimiter is not
// required), writes one "\n"-terminated JSON response and lets the caller close the connection.
func (p *Proxy) handleConn(ctx context.Context, conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(requestReadTimeout))
	var raw map[string]json.RawMessage
	decodeErr := json.NewDecoder(conn).Decode(&raw)
	_ = conn.SetReadDeadline(time.Time{})

	var resp any
	action := ""
	if decodeErr != nil {
		p.logger.Warn("Failed to decode request", zap.Error(decodeErr))
		resp = newProtocolError(errInvalidParams, "malformed request: %v", decodeErr)
	} else if rawAction, ok := raw["action"]; !ok || json.Unmarshal(rawAction, &action) != nil {
		resp = newProtocolError(errInvalidAction, "field 'action' is required and must be a string")
	} else {
		resp = p.dispatch(ctx, action, raw)
	}

	if perr, ok := resp.(*protocolError); ok {
		p.logger.Warn("request error", zap.String("action", action), zap.String("code", perr.Code), zap.String("message", perr.Message))
	}

	w := bufio.NewWriter(conn)
	_ = conn.SetWriteDeadline(time.Now().Add(responseTimeout))
	if err := json.NewEncoder(w).Encode(resp); err == nil {
		err = w.Flush()
		if err != nil {
			p.logger.Error("Failed to send response", zap.String("action", action), zap.Error(err))
		}
	} else {
		p.logger.Error("Failed to encode response", zap.String("action", action), zap.Error(err))
	}
}

func (p *Proxy) dispatch(ctx context.Context, action string, raw map[string]json.RawMessage) any {
	switch action {
	case "exec":
		// legacy flat error format {"error": "..."} is kept for exec
		var req execRequest
		if err := decodeParams(raw, &req); err != nil {
			return map[string]string{"error": err.Error()}
		}
		if req.Command == "" {
			return map[string]string{"error": "missing command"}
		}
		resp, err := p.exec(ctx, req.Command, req.Timeout)
		if err != nil {
			return map[string]string{"error": err.Error()}
		}
		return resp
	case "start":
		var req startRequest
		if err := decodeParams(raw, &req); err != nil {
			return newProtocolError(errInvalidParams, "%v", err)
		}
		return p.handleStart(req)
	case "wait", "peek":
		var req pollRequest
		if err := decodeParams(raw, &req); err != nil {
			return newProtocolError(errInvalidParams, "%v", err)
		}
		return p.handlePoll(ctx, req, action == "wait")
	case "kill":
		var req killRequest
		if err := decodeParams(raw, &req); err != nil {
			return newProtocolError(errInvalidParams, "%v", err)
		}
		return p.handleKill(ctx, req)
	case "status":
		st, err := p.status(ctx)
		if err != nil {
			return map[string]string{"error": err.Error()}
		}
		return st
	case "destroy":
		if err := p.destroy(ctx); err != nil {
			return map[string]string{"error": err.Error()}
		}
		return map[string]string{"result": "ok"}
	default:
		return newProtocolError(errInvalidAction, "unknown action: %s", action)
	}
}

// decodeParams decodes the request fields into a typed struct, reporting type mismatches
func decodeParams(raw map[string]json.RawMessage, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return fmt.Errorf("field '%s' must be of type %s", typeErr.Field, typeErr.Type)
		}
		return err
	}
	return nil
}

func (p *Proxy) handleStart(req startRequest) any {
	if req.Command == nil || *req.Command == "" {
		return newProtocolError(errInvalidParams, "field 'command' is required")
	}

	lifetime := jobDefaultMaxLifetime
	if req.MaxLifetime != nil {
		if *req.MaxLifetime <= 0 {
			return newProtocolError(errInvalidParams, "field 'max_lifetime' must be positive")
		}
		lifetime = min(*req.MaxLifetime, jobMaxLifetimeCap)
	}

	env, perr := envList(req.Env)
	if perr != nil {
		return perr
	}

	p.jobsMu.Lock()
	parent := p.jobsCtx
	p.jobsMu.Unlock()

	j := newJob(parent, *req.Command, req.Cwd, env, jobOutputLimit)
	if !p.registerJob(j) {
		j.cancel()
		return newProtocolError(errProxyBusy, "too many running jobs (limit %d)", p.maxJobs)
	}
	p.launchJob(j, time.Duration(lifetime)*time.Second)

	p.logger.Info("job accepted", zap.String("jobID", j.id), zap.String("command", j.command), zap.Int("maxLifetime", lifetime))
	return startResponse{JobID: j.id}
}

func envList(env map[string]string) ([]string, *protocolError) {
	list := make([]string, 0, len(env))
	for k, v := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, newProtocolError(errInvalidParams, "invalid environment variable name %q", k)
		}
		list = append(list, k+"="+v)
	}
	sort.Strings(list)
	return list, nil
}

func (p *Proxy) handlePoll(ctx context.Context, req pollRequest, block bool) any {
	if req.JobID == "" {
		return newProtocolError(errInvalidParams, "field 'job_id' is required")
	}
	if req.StdoutOffset < 0 || req.StderrOffset < 0 {
		return newProtocolError(errInvalidParams, "offsets must not be negative")
	}
	j := p.getJob(req.JobID)
	if j == nil {
		return newProtocolError(errJobNotFound, "Job %s not found", req.JobID)
	}

	if block {
		timeout := jobDefaultWaitTimeout
		if req.Timeout != nil {
			timeout = max(0, min(*req.Timeout, jobWaitTimeoutCap))
		}
		select {
		case <-j.done:
		case <-time.After(time.Duration(timeout) * time.Second):
		case <-ctx.Done():
		}
	}

	_ = p.updateLastActive(time.Now())
	return j.poll(req.StdoutOffset, req.StderrOffset)
}

func (p *Proxy) handleKill(ctx context.Context, req killRequest) any {
	if req.JobID == "" {
		return newProtocolError(errInvalidParams, "field 'job_id' is required")
	}
	signal := req.Signal
	if signal == "" {
		signal = "TERM"
	}
	if signal != "TERM" && signal != "KILL" {
		return newProtocolError(errInvalidParams, "field 'signal' must be \"TERM\" or \"KILL\"")
	}
	j := p.getJob(req.JobID)
	if j == nil {
		return newProtocolError(errJobNotFound, "Job %s not found", req.JobID)
	}

	if exited, exitCode := j.state(); exited {
		return killResponse{JobID: j.id, Status: "already_exited", ExitCode: &exitCode}
	}

	if !p.signalJob(ctx, j, signal) {
		// the process was gone already: the job is finishing on its own
		select {
		case <-j.done:
			_, exitCode := j.state()
			return killResponse{JobID: j.id, Status: "already_exited", ExitCode: &exitCode}
		case <-time.After(time.Second):
		}
	}

	p.logger.Info("job killed", zap.String("jobID", j.id), zap.String("signal", signal))
	return killResponse{JobID: j.id, Status: "killed"}
}

// exec runs a command synchronously. On timeout the command is killed and exit code 124 is returned.
func (p *Proxy) exec(ctx context.Context, command string, timeoutSeconds int) (*execResponse, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = p.timeoutSeconds
	}
	timeoutSeconds = min(timeoutSeconds, execMaxTimeout)

	p.jobsMu.Lock()
	parent := p.jobsCtx
	p.jobsMu.Unlock()

	p.execs.Add(1)
	defer p.execs.Add(-1)

	j := newJob(parent, command, "", nil, execOutputLimit)
	p.launchJob(j, 0)

	timedOut := false
	select {
	case <-j.done:
	case <-time.After(time.Duration(timeoutSeconds) * time.Second):
		timedOut = true
	case <-ctx.Done():
		timedOut = true
	}
	if timedOut {
		p.logger.Warn("Command execution timed out", zap.String("command", command), zap.Int("timeout", timeoutSeconds))
		p.forceKill(j)
		<-j.done
	}

	j.mu.Lock()
	exitCode, infraErr := j.exitCode, j.infraErr
	j.mu.Unlock()
	if infraErr != nil {
		return nil, infraErr
	}

	stdout, _, _ := j.stdout.read(0, true)
	stderr, _, _ := j.stderr.read(0, true)
	resp := &execResponse{Stdout: string(stdout), Stderr: string(stderr), ExitCode: exitCode}
	if timedOut {
		resp.Stderr += fmt.Sprintf("\n[Command timed out after %d seconds]", timeoutSeconds)
		resp.ExitCode = 124
	}
	return resp, nil
}

// readyContainer returns a running container with a ready Docker daemon, creating it if needed
func (p *Proxy) readyContainer(ctx context.Context) (string, error) {
	p.mu.Lock()
	containerID, err := p.ensureContainer(ctx)
	p.mu.Unlock()
	if err != nil {
		return "", err
	}

	if err := p.waitForDockerDaemon(ctx, containerID); err != nil {
		p.logger.Warn("Docker daemon might not be ready, continuing anyway", zap.Error(err))
	}
	_ = p.updateLastActive(time.Now())
	return containerID, nil
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
	p.removeMeta()
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
	p.metaMu.Lock()
	err = p.writeMeta(meta)
	p.metaMu.Unlock()
	if err != nil {
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
		// Try to run docker version command to check if daemon is ready
		stdout, stderr, exitCode, err := p.dockerClient.ExecInContainer(ctx, containerID, []string{"sh", "-c", "docker version >/dev/null 2>&1"}, 5)
		if err == nil && exitCode == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for Docker daemon: stdout=%s, stderr=%s, exitCode=%d", stdout, stderr, exitCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
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

// writeMeta atomically replaces the meta file, so concurrent readers never see a partial write
func (p *Proxy) writeMeta(m *proxyMeta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := p.metaPath + ".tmp"
	if err := ioutil.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.metaPath)
}

func (p *Proxy) removeMeta() {
	p.metaMu.Lock()
	defer p.metaMu.Unlock()
	_ = os.Remove(p.metaPath)
}

func (p *Proxy) updateLastActive(t time.Time) error {
	p.metaMu.Lock()
	defer p.metaMu.Unlock()

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
			p.collectJobs(time.Now())
			if p.hasRunningJobs() {
				// a background job may run silently for up to max_lifetime
				_ = p.updateLastActive(time.Now())
				continue
			}

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
				p.removeMeta()
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
