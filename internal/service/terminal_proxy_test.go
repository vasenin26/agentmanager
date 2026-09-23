package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// localDocker runs "container" commands directly on the host, so that the proxy (including its
// wrapper and kill shell scripts) can be tested without Docker.
type localDocker struct {
	docker.DockerClient
}

func (localDocker) PullImage(context.Context, string, docker.AuthConfig) error { return nil }
func (localDocker) CreateContainer(context.Context, docker.ContainerConfig) (string, error) {
	return "local", nil
}
func (localDocker) StartContainer(context.Context, string) error  { return nil }
func (localDocker) StopContainer(context.Context, string) error   { return nil }
func (localDocker) RemoveContainer(context.Context, string) error { return nil }
func (localDocker) InspectContainer(_ context.Context, id string) (docker.ContainerInspect, error) {
	return docker.ContainerInspect{ID: id, State: "running"}, nil
}

func (localDocker) ExecInContainer(ctx context.Context, _ string, cmd []string, timeoutSeconds int) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	var stdout, stderr strings.Builder
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

func (localDocker) ExecStream(ctx context.Context, _ string, opts docker.ExecStreamOptions) (int, error) {
	c := exec.Command(opts.Cmd[0], opts.Cmd[1:]...)
	c.Env = append(os.Environ(), opts.Env...)
	c.Stdout, c.Stderr = opts.Stdout, opts.Stderr
	if err := c.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		if err != nil {
			return -1, err
		}
		return 0, nil
	case <-ctx.Done():
		// like the real client: detach from the stream, leave the process alone
		return -1, ctx.Err()
	}
}

type proxyHarness struct {
	t      *testing.T
	proxy  *Proxy
	socket string
}

func startProxy(t *testing.T, dc docker.DockerClient, image string) *proxyHarness {
	t.Helper()

	socketDir, err := os.MkdirTemp("", "cp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	t.Setenv("ORCHESTRATOR_SOCKET_DIR", socketDir)

	id := fmt.Sprintf("test-%d", time.Now().UnixNano())
	p := NewProxy(id, dc, image, t.TempDir(), docker.AuthConfig{}, zap.NewNop())
	t.Cleanup(func() { os.Remove(p.metaPath) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = p.Serve(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(p.socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("proxy socket was not created")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &proxyHarness{t: t, proxy: p, socket: p.socketPath}
}

// sendRaw writes body as-is (no trailing newline, like the PHP client), reads one response line
// and checks that the server closes the connection after it.
func (h *proxyHarness) sendRaw(body string, timeout time.Duration) map[string]any {
	h.t.Helper()
	conn, err := net.Dial("unix", h.socket)
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := io.WriteString(conn, body); err != nil {
		h.t.Fatalf("write: %v", err)
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		h.t.Fatalf("read response to %s: %v", body, err)
	}
	if _, err := r.ReadByte(); err != io.EOF {
		h.t.Errorf("server must close the connection after the response, got %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		h.t.Fatalf("bad JSON response %q: %v", line, err)
	}
	return resp
}

func (h *proxyHarness) send(req map[string]any) map[string]any {
	h.t.Helper()
	b, _ := json.Marshal(req)
	return h.sendRaw(string(b), 40*time.Second)
}

func (h *proxyHarness) start(command string, extra map[string]any) string {
	h.t.Helper()
	req := map[string]any{"action": "start", "command": command}
	for k, v := range extra {
		req[k] = v
	}
	resp := h.send(req)
	id, _ := resp["job_id"].(string)
	if id == "" {
		h.t.Fatalf("start failed: %v", resp)
	}
	return id
}

// waitExit polls with wait until the job exits and returns the concatenated output and last response
func (h *proxyHarness) waitExit(jobID string, within time.Duration) (string, string, map[string]any) {
	h.t.Helper()
	var stdout, stderr strings.Builder
	var so, se float64
	deadline := time.Now().Add(within)
	for {
		resp := h.send(map[string]any{"action": "wait", "job_id": jobID, "timeout": 1, "stdout_offset": so, "stderr_offset": se})
		if resp["error"] != nil {
			h.t.Fatalf("wait failed: %v", resp)
		}
		stdout.WriteString(resp["stdout_delta"].(string))
		stderr.WriteString(resp["stderr_delta"].(string))
		so, se = resp["stdout_offset"].(float64), resp["stderr_offset"].(float64)
		if resp["status"] == "exited" {
			return stdout.String(), stderr.String(), resp
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("job %s did not exit within %s", jobID, within)
		}
	}
}

func expectError(t *testing.T, resp map[string]any, code string) {
	t.Helper()
	if resp["error"] != code {
		t.Errorf("expected error %q, got %v", code, resp)
	}
	if code != "" && resp["message"] == nil {
		t.Errorf("error response must carry a message: %v", resp)
	}
}

func processAlive(t *testing.T, pattern string) bool {
	t.Helper()
	return exec.Command("pgrep", "-f", pattern).Run() == nil
}

func TestProxyExec(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	resp := h.sendRaw(`{"action":"exec","command":"echo out; echo err >&2; exit 3"}`, 10*time.Second)
	if resp["stdout"] != "out\n" || resp["stderr"] != "err\n" || resp["exit_code"] != float64(3) {
		t.Errorf("unexpected exec response: %v", resp)
	}

	resp = h.send(map[string]any{"action": "exec"})
	if resp["error"] != "missing command" {
		t.Errorf("expected legacy flat error, got %v", resp)
	}
}

func TestProxyExecTimeoutKillsCommand(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	started := time.Now()
	resp := h.send(map[string]any{"action": "exec", "command": "echo begin; sleep 4101", "timeout": 1})
	if resp["exit_code"] != float64(124) || resp["stdout"] != "begin\n" {
		t.Errorf("unexpected timeout response: %v", resp)
	}
	if time.Since(started) > 5*time.Second {
		t.Errorf("exec timeout took too long: %s", time.Since(started))
	}
	time.Sleep(200 * time.Millisecond)
	if processAlive(t, "sleep 4101") {
		t.Error("timed out command is still running")
	}
}

func TestProxyStartWaitPeek(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	id := h.start(`echo "$FOO"; pwd; echo warn >&2; sleep 0.3; echo done; exit 7`, map[string]any{
		"cwd": "/tmp",
		"env": map[string]string{"FOO": "bar baz"},
	})

	stdout, stderr, last := h.waitExit(id, 10*time.Second)
	if stdout != "bar baz\n/tmp\ndone\n" || stderr != "warn\n" {
		t.Errorf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
	}
	if last["exit_code"] != float64(7) || last["truncated"] != false || last["job_id"] != id {
		t.Errorf("unexpected final response: %v", last)
	}

	// peek from the returned offsets gives nothing new
	resp := h.send(map[string]any{"action": "peek", "job_id": id, "stdout_offset": last["stdout_offset"], "stderr_offset": last["stderr_offset"]})
	if resp["status"] != "exited" || resp["stdout_delta"] != "" || resp["stderr_delta"] != "" {
		t.Errorf("unexpected peek: %v", resp)
	}
	// peek from zero gives everything again
	resp = h.send(map[string]any{"action": "peek", "job_id": id})
	if resp["stdout_delta"] != "bar baz\n/tmp\ndone\n" {
		t.Errorf("unexpected peek from zero: %v", resp)
	}

	id = h.start("exit 0", map[string]any{"cwd": "/nonexistent-dir"})
	_, stderr, last = h.waitExit(id, 5*time.Second)
	if last["exit_code"] != float64(1) || stderr == "" {
		t.Errorf("start in missing cwd must fail: %v, stderr=%q", last, stderr)
	}
}

func TestProxyWaitTimeoutAndKill(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	// the tree: sh -lc -> (sleep 4201 in background, sleep 4202 in foreground)
	id := h.start("sleep 4201 & sleep 4202; wait", nil)

	started := time.Now()
	resp := h.send(map[string]any{"action": "wait", "job_id": id, "timeout": 1})
	if resp["status"] != "running" || resp["exit_code"] != nil {
		t.Errorf("expected running job, got %v", resp)
	}
	if _, ok := resp["exit_code"]; !ok {
		t.Error("exit_code must be present (null) for a running job")
	}
	if d := time.Since(started); d < 900*time.Millisecond || d > 3*time.Second {
		t.Errorf("wait must block for its timeout, took %s", d)
	}

	resp = h.send(map[string]any{"action": "peek", "job_id": id, "timeout": 20})
	if resp["status"] != "running" {
		t.Errorf("peek must not block: %v", resp)
	}

	resp = h.send(map[string]any{"action": "kill", "job_id": id})
	if resp["status"] != "killed" || resp["exit_code"] != nil || resp["job_id"] != id {
		t.Errorf("unexpected kill response: %v", resp)
	}

	_, _, last := h.waitExit(id, 5*time.Second)
	if last["exit_code"] != float64(143) {
		t.Errorf("expected exit code 143 after TERM, got %v", last["exit_code"])
	}
	time.Sleep(200 * time.Millisecond)
	if processAlive(t, "sleep 420[12]") {
		t.Error("processes of the killed job are still running")
	}

	resp = h.send(map[string]any{"action": "kill", "job_id": id, "signal": "KILL"})
	if resp["status"] != "already_exited" || resp["exit_code"] != float64(143) {
		t.Errorf("unexpected kill of exited job: %v", resp)
	}
}

func TestProxyMaxLifetime(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	started := time.Now()
	id := h.start("trap '' TERM; sleep 4301", map[string]any{"max_lifetime": 1})
	_, _, last := h.waitExit(id, 10*time.Second)
	if last["exit_code"] != float64(137) {
		t.Errorf("expected SIGKILL exit code 137, got %v", last)
	}
	if d := time.Since(started); d > 5*time.Second {
		t.Errorf("max_lifetime was not enforced in time: %s", d)
	}
	if processAlive(t, "sleep 4301") {
		t.Error("job outlived max_lifetime")
	}
}

func TestProxyBusy(t *testing.T) {
	h := startProxy(t, localDocker{}, "")
	h.proxy.maxJobs = 1

	id := h.start("sleep 4401", nil)
	expectError(t, h.send(map[string]any{"action": "start", "command": "true"}), errProxyBusy)

	h.send(map[string]any{"action": "kill", "job_id": id, "signal": "KILL"})
	h.waitExit(id, 5*time.Second)
	h.start("true", nil)
}

func TestProxyErrors(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	expectError(t, h.send(map[string]any{"action": "nope"}), errInvalidAction)
	expectError(t, h.send(map[string]any{"command": "ls"}), errInvalidAction)
	expectError(t, h.sendRaw(`{"action":`, 15*time.Second), errInvalidParams)

	expectError(t, h.send(map[string]any{"action": "start"}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "start", "command": 5}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "start", "command": "ls", "max_lifetime": 0}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "start", "command": "ls", "env": map[string]any{"A": 1}}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "start", "command": "ls", "env": map[string]any{"A=B": "1"}}), errInvalidParams)

	expectError(t, h.send(map[string]any{"action": "wait"}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "wait", "job_id": "x", "stdout_offset": "0"}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "peek", "job_id": "x", "stderr_offset": -1}), errInvalidParams)
	expectError(t, h.send(map[string]any{"action": "wait", "job_id": "missing"}), errJobNotFound)
	expectError(t, h.send(map[string]any{"action": "peek", "job_id": "missing"}), errJobNotFound)

	expectError(t, h.send(map[string]any{"action": "kill", "job_id": "missing"}), errJobNotFound)
	expectError(t, h.send(map[string]any{"action": "kill", "job_id": "missing", "signal": "HUP"}), errInvalidParams)
}

func TestProxyCollectsFinishedJobs(t *testing.T) {
	h := startProxy(t, localDocker{}, "")

	id := h.start("true", nil)
	h.waitExit(id, 5*time.Second)

	h.proxy.collectJobs(time.Now())
	if h.proxy.getJob(id) == nil {
		t.Fatal("a just finished job must stay pollable")
	}
	h.proxy.collectJobs(time.Now().Add(jobRetention + time.Minute))
	expectError(t, h.send(map[string]any{"action": "peek", "job_id": id}), errJobNotFound)
}

func TestOutputBuffer(t *testing.T) {
	b := newOutputBuffer(8)
	b.Write([]byte("abcdef"))

	data, off, truncated := b.read(0, false)
	if string(data) != "abcdef" || off != 6 || truncated {
		t.Errorf("got %q %d %v", data, off, truncated)
	}

	b.Write([]byte("ghij")) // "abcdefghij" -> keeps "cdefghij"
	data, off, truncated = b.read(6, false)
	if string(data) != "ghij" || off != 10 || truncated {
		t.Errorf("got %q %d %v", data, off, truncated)
	}
	data, off, truncated = b.read(0, false)
	if string(data) != "cdefghij" || off != 10 || !truncated {
		t.Errorf("dropped data must be reported: %q %d %v", data, off, truncated)
	}
	data, off, _ = b.read(100, false)
	if len(data) != 0 || off != 10 {
		t.Errorf("offset past the end must be clamped: %q %d", data, off)
	}
}

func TestOutputBufferUTF8(t *testing.T) {
	b := newOutputBuffer(1024)
	word := []byte("привет")
	b.Write(word[:3]) // "п" + half of "р"

	data, off, _ := b.read(0, false)
	if string(data) != "п" || off != 2 {
		t.Errorf("incomplete character must be held back: %q %d", data, off)
	}
	b.Write(word[3:])
	data, off, _ = b.read(off, false)
	if string(data) != "ривет" || off != int64(len(word)) {
		t.Errorf("got %q %d", data, off)
	}

	b.Write([]byte{0xd0})
	if data, _, _ := b.read(off, true); len(data) != 1 {
		t.Error("final read must return everything")
	}

	// truncation in the middle of a character skips its remainder
	b = newOutputBuffer(5)
	b.Write([]byte("ббб")) // 6 bytes, the first byte is dropped
	data, off, truncated := b.read(0, false)
	if string(data) != "бб" || off != 6 || !truncated {
		t.Errorf("got %q %d %v", data, off, truncated)
	}
}

// TestProxyDinD runs the protocol against a real docker:dind container.
func TestProxyDinD(t *testing.T) {
	if testing.Short() || os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping docker tests")
	}
	dc, err := docker.New(zap.NewNop())
	if err != nil {
		t.Skipf("Docker is not available: %v", err)
	}
	defer dc.Close()
	if _, err := dc.ListRunnedContainers(context.Background()); err != nil {
		t.Skipf("Docker is not available: %v", err)
	}

	h := startProxy(t, dc, "docker:dind")
	defer h.proxy.destroy(context.Background())

	resp := h.sendRaw(`{"action":"exec","command":"echo hi; ls -d /opt/repos; exit 5"}`, 3*time.Minute)
	if resp["stdout"] != "hi\n/opt/repos\n" || resp["exit_code"] != float64(5) {
		t.Fatalf("unexpected exec response: %v", resp)
	}

	id := h.start("echo \"$FOO\"; pwd; sleep 0.5; echo done", map[string]any{"cwd": "/opt/repos", "env": map[string]string{"FOO": "bar"}})
	stdout, _, last := h.waitExit(id, 30*time.Second)
	if stdout != "bar\n/opt/repos\ndone\n" || last["exit_code"] != float64(0) {
		t.Errorf("unexpected job result: %q %v", stdout, last)
	}

	id = h.start("sleep 4501 & sleep 4502; wait", nil)
	time.Sleep(500 * time.Millisecond)
	resp = h.send(map[string]any{"action": "kill", "job_id": id, "signal": "KILL"})
	if resp["status"] != "killed" {
		t.Errorf("unexpected kill response: %v", resp)
	}
	_, _, last = h.waitExit(id, 15*time.Second)
	if last["exit_code"] != float64(137) {
		t.Errorf("expected 137, got %v", last)
	}
	resp = h.send(map[string]any{"action": "exec", "command": "ps | grep -c 'sleep 450[12]' || true"})
	if strings.TrimSpace(resp["stdout"].(string)) != "0" {
		t.Errorf("killed job left processes behind: %v", resp)
	}

	resp = h.send(map[string]any{"action": "exec", "command": "sleep 4601", "timeout": 1})
	if resp["exit_code"] != float64(124) {
		t.Errorf("expected timeout exit code, got %v", resp)
	}
}
