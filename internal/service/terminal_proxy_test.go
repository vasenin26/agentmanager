package service

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// TestTerminalProxySequentialCommands tests that proxy can execute sequential commands
// in a persistent bash session: pwd, cd /tmp, pwd, ls
func TestTerminalProxySequentialCommands(t *testing.T) {
	// Skip if docker is not available
	if os.Getenv("SKIP_DOCKER_TESTS") == "true" {
		t.Skip("Skipping docker tests")
	}

	// Create logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Create docker client
	dc, err := docker.New(logger)
	if err != nil {
		t.Fatalf("Failed to create docker client: %v", err)
	}
	defer dc.Close()

	// Check if Docker is available
	ctx := context.Background()
	containers, err := dc.ListRunnedContainers(ctx)
	if err != nil {
		t.Skipf("Docker is not available: %v", err)
	}
	_ = containers // Just checking connectivity

	// Use docker:dind image (DinD - Docker in Docker) as required by the proxy
	testImage := "docker:dind"
	// Check if we need to pull image
	if err := dc.PullImage(ctx, testImage, docker.AuthConfig{}); err != nil {
		t.Logf("Warning: Failed to pull image, trying to use existing: %v", err)
	}

	// Create test volume or use temp dir
	testVolume := filepath.Join(os.TempDir(), "test_terminal_proxy_volume")
	_ = os.MkdirAll(testVolume, 0o755)
	defer os.RemoveAll(testVolume)

	// Create proxy with test ID
	testID := "test-proxy-sequential"
	proxy := NewProxy(testID, dc, testImage, testVolume, docker.AuthConfig{}, logger)

	// Clean up meta file if exists
	metaPath := filepath.Join("data", "command_proxy", testID+".json")
	_ = os.Remove(metaPath)
	defer os.Remove(metaPath)

	// Calculate socket path (same as in NewProxy)
	socketDir := os.Getenv("ORCHESTRATOR_SOCKET_DIR")
	if socketDir == "" {
		socketDir = "/tmp/orchestrator"
	}
	socketPath := filepath.Join(socketDir, testID+".sock")
	_ = os.MkdirAll(filepath.Dir(socketPath), 0o755)
	defer os.Remove(socketPath)

	// Start proxy in background
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- proxy.Serve(ctxWithCancel)
	}()

	// Wait a bit for proxy to start
	time.Sleep(1 * time.Second)

	// Connect to socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect to socket: %v", err)
	}
	defer conn.Close()

	// Helper function to send command and receive response
	sendCommand := func(cmd string) (*execResponse, error) {
		req := request{
			Action:  "exec",
			Command: cmd,
		}

		enc := json.NewEncoder(conn)
		if err := enc.Encode(req); err != nil {
			return nil, err
		}

		dec := json.NewDecoder(conn)
		var resp execResponse
		if err := dec.Decode(&resp); err != nil {
			return nil, err
		}

		return &resp, nil
	}

	// Test 1: pwd - should show initial directory
	t.Log("Executing: pwd")
	resp1, err := sendCommand("pwd")
	if err != nil {
		t.Fatalf("Failed to execute pwd: %v", err)
	}
	if resp1.ExitCode != 0 {
		t.Errorf("pwd failed with exit code %d, stderr: %s", resp1.ExitCode, resp1.Stderr)
	}
	initialDir := strings.TrimSpace(resp1.Stdout)
	t.Logf("Initial directory: %s", initialDir)
	if initialDir == "" {
		t.Error("pwd returned empty output")
	}

	// Test 2: cd /tmp - change directory
	t.Log("Executing: cd /tmp")
	resp2, err := sendCommand("cd /tmp")
	if err != nil {
		t.Fatalf("Failed to execute cd /tmp: %v", err)
	}
	// cd usually doesn't produce output, but may have stderr
	t.Logf("cd /tmp stdout: %q, stderr: %q", resp2.Stdout, resp2.Stderr)

	// Test 3: pwd - should now show /tmp
	t.Log("Executing: pwd (after cd)")
	resp3, err := sendCommand("pwd")
	if err != nil {
		t.Fatalf("Failed to execute pwd after cd: %v", err)
	}
	if resp3.ExitCode != 0 {
		t.Errorf("pwd after cd failed with exit code %d, stderr: %s", resp3.ExitCode, resp3.Stderr)
	}
	newDir := strings.TrimSpace(resp3.Stdout)
	t.Logf("Directory after cd: %s", newDir)
	if !strings.Contains(newDir, "/tmp") {
		t.Errorf("Expected directory to contain /tmp, got: %s", newDir)
	}

	// Test 4: ls - list files in /tmp
	t.Log("Executing: ls")
	resp4, err := sendCommand("ls")
	if err != nil {
		t.Fatalf("Failed to execute ls: %v", err)
	}
	if resp4.ExitCode != 0 {
		t.Errorf("ls failed with exit code %d, stderr: %s", resp4.ExitCode, resp4.Stderr)
	}
	t.Logf("ls output: %s", resp4.Stdout)
	// ls output may be empty, but should not error

	// Verify that all commands were executed in the same bash session
	// by checking that directory change persisted
	t.Log("Verifying session persistence...")
	resp5, err := sendCommand("pwd")
	if err != nil {
		t.Fatalf("Failed to execute final pwd: %v", err)
	}
	finalDir := strings.TrimSpace(resp5.Stdout)
	if finalDir != newDir {
		t.Errorf("Session not persistent: expected %s, got %s", newDir, finalDir)
	}

	t.Log("All sequential commands executed successfully in persistent bash session")

	// Clean up: send destroy request
	destroyReq := request{
		Action: "destroy",
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(destroyReq); err != nil {
		t.Logf("Warning: Failed to send destroy request: %v", err)
	} else {
		dec := json.NewDecoder(conn)
		var destroyResp map[string]string
		if err := dec.Decode(&destroyResp); err != nil {
			t.Logf("Warning: Failed to read destroy response: %v", err)
		} else {
			t.Logf("Destroy response: %v", destroyResp)
		}
	}
}
