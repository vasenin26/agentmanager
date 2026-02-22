package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vasenin26/agentmanager/internal/docker"
	"github.com/vasenin26/agentmanager/internal/service"
	"go.uber.org/zap"
)

const (
	proxyID   = "dind-cli"
	dindImage = "docker:dind"
)

// Protocol types (same JSON as service.request / execResponse)
type execReq struct {
	Action  string `json:"action"`
	Command string `json:"command,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// ANSI color codes
const (
	ansiReset   = "\033[0m"
	ansiKey     = "\033[36m"  // cyan
	ansiString  = "\033[32m"  // green
	ansiNumber  = "\033[35m"  // magenta
	ansiBool    = "\033[33m"  // yellow
	ansiNull    = "\033[2m"   // dim
)

func isTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		info, err := f.Stat()
		return err == nil && (info.Mode()&os.ModeCharDevice) != 0
	}
	return false
}

// writeColorJSON recursively writes JSON with ANSI syntax highlighting.
func writeColorJSON(w io.Writer, v interface{}, indent string) {
	switch x := v.(type) {
	case map[string]interface{}:
		fmt.Fprint(w, "{\n")
		inner := indent + "  "
		first := true
		for k, val := range x {
			if !first {
				fmt.Fprint(w, ",\n")
			}
			first = false
			fmt.Fprintf(w, "%s%s%q%s: ", inner, ansiKey, k, ansiReset)
			writeColorJSON(w, val, inner)
		}
		fmt.Fprintf(w, "\n%s}", indent)
	case []interface{}:
		fmt.Fprint(w, "[\n")
		inner := indent + "  "
		for i, val := range x {
			if i > 0 {
				fmt.Fprint(w, ",\n")
			}
			fmt.Fprint(w, inner)
			writeColorJSON(w, val, inner)
		}
		fmt.Fprintf(w, "\n%s]", indent)
	case string:
		fmt.Fprintf(w, "%s%q%s", ansiString, x, ansiReset)
	case float64:
		fmt.Fprintf(w, "%s%v%s", ansiNumber, x, ansiReset)
	case bool:
		fmt.Fprintf(w, "%s%v%s", ansiBool, x, ansiReset)
	case nil:
		fmt.Fprintf(w, "%snull%s", ansiNull, ansiReset)
	default:
		fmt.Fprintf(w, "%v", x)
	}
}

func colorizeJSON(w io.Writer, src string) {
	var v interface{}
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		fmt.Fprint(w, src)
		return
	}
	writeColorJSON(w, v, "")
}

func formatAndPrintJSON(w io.Writer, raw []byte, prefix string, useColor bool) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		fmt.Fprintf(w, "%s%s\n", prefix, string(raw))
		return
	}
	formatted := buf.String()
	fmt.Fprint(w, prefix)
	if useColor {
		colorizeJSON(w, formatted)
	} else {
		fmt.Fprint(w, formatted)
	}
	fmt.Fprintln(w)
}

func socketPath() string {
	socketDir := os.Getenv("ORCHESTRATOR_SOCKET_DIR")
	if socketDir == "" {
		socketDir = "/tmp/orchestrator"
	}
	return filepath.Join(socketDir, proxyID+".sock")
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		// Socket file exists; ensure proxy is accepting (handles stale socket from previous run)
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for socket: %s", path)
}

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logger:", err)
		os.Exit(1)
	}
	defer logger.Sync()

	dc, err := docker.New(logger)
	if err != nil {
		logger.Fatal("failed to create docker client", zap.Error(err))
	}
	defer dc.Close()

	projectDir := os.Getenv("DIND_PROJECT_PATH")
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			logger.Fatal("failed to get current directory", zap.Error(err))
		}
	}
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		logger.Fatal("failed to resolve project path", zap.Error(err))
	}
	logger.Info("binding project directory into dind", zap.String("path", projectDir))

	proxy := service.NewProxy(proxyID, dc, dindImage, projectDir, docker.AuthConfig{}, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := proxy.Serve(ctx); err != nil && ctx.Err() == nil {
			logger.Error("proxy serve error", zap.Error(err))
		}
	}()

	sockPath := socketPath()
	if err := waitForSocket(sockPath, 10*time.Second); err != nil {
		logger.Fatal("socket not ready", zap.String("path", sockPath), zap.Error(err))
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		logger.Fatal("dial socket", zap.Error(err))
	}
	defer conn.Close()

	useColor := isTTY(os.Stdout)
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Fprintln(os.Stdout, "DinD CLI: type a command and press Enter (exit/quit to stop).")
	for {
		fmt.Fprint(os.Stdout, "> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		req := execReq{Action: "exec", Command: line, Timeout: 60}
		reqCompact, err := json.Marshal(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal request:", err)
			continue
		}
		formatAndPrintJSON(os.Stdout, reqCompact, "→ sent:\n", useColor)

		if err := enc.Encode(req); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
			break
		}

		var respRaw json.RawMessage
		if err := dec.Decode(&respRaw); err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintln(os.Stderr, "receive:", err)
			break
		}
		formatAndPrintJSON(os.Stdout, respRaw, "← received:\n", useColor)
	}
}
