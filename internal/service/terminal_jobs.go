package service

import (
	"context"
	"fmt"
	"path"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/vasenin26/agentmanager/internal/docker"
	"go.uber.org/zap"
)

// Limits of the Command Proxy protocol (agentmodule/docs/command-proxy-protocol.md)
const (
	jobDefaultMaxLifetime = 600  // seconds
	jobMaxLifetimeCap     = 3600 // seconds
	jobDefaultWaitTimeout = 15   // seconds
	jobWaitTimeoutCap     = 30   // seconds

	jobOutputLimit     = 64 * 1024  // bytes kept per stream of a background job
	execOutputLimit    = 256 * 1024 // bytes kept per stream of a synchronous exec
	jobMaxConcurrent   = 16
	jobRetention       = 10 * time.Minute // how long a finished job stays pollable
	jobKillGracePeriod = 5 * time.Second  // after SIGKILL, how long to wait for the stream to close

	jobPidDir = "/tmp/.command-proxy"
)

// jobWrapperScript runs the user command as a child of a small shell that records the child's
// PID in a file, so that the command's process tree can be signalled later from a separate exec.
// Positional args: $1 - command, $2 - pid file, $3 - working directory (may be empty).
const jobWrapperScript = `if [ -n "$3" ]; then cd -- "$3" || exit 1; fi
mkdir -p -- "${2%/*}"
sh -lc "$1" </dev/null &
pid=$!
echo "$pid" > "$2"
wait "$pid"
rc=$?
rm -f -- "$2"
exit "$rc"`

// jobKillScript signals the whole process tree of a job. The tree is frozen with SIGSTOP while
// it is being collected so that it cannot fork away, then the signal is delivered and the tree
// is resumed. Uses only POSIX sh + awk + /proc, so it works with busybox (docker:dind is Alpine).
// Positional args: $1 - pid file, $2 - signal name. Exits 3 if the job process is not running.
const jobKillScript = `pf=$1; sig=$2; i=0
while [ ! -s "$pf" ] && [ "$i" -lt 10 ]; do sleep 0.1; i=$((i+1)); done
[ -s "$pf" ] || exit 3
root=$(cat "$pf")
kill -STOP "$root" 2>/dev/null || exit 3
all=" $root "
while :; do
  new=$(cat /proc/[0-9]*/status 2>/dev/null | awk -v set="$all" '/^Pid:/ {p=$2} /^PPid:/ { if (index(set, " " $2 " ") && !index(set, " " p " ")) print p }' | sort -u)
  [ -z "$new" ] && break
  kill -STOP $new 2>/dev/null
  all="$all$(echo $new) "
done
kill -"$sig" $all 2>/dev/null
kill -CONT $all 2>/dev/null
exit 0`

// outputBuffer is a bounded, concurrency-safe byte buffer addressed by absolute offsets.
// When the limit is exceeded, the oldest data is dropped.
type outputBuffer struct {
	mu    sync.Mutex
	data  []byte
	start int64 // absolute offset of data[0]
	limit int
}

func newOutputBuffer(limit int) *outputBuffer {
	return &outputBuffer{limit: limit}
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)
	if over := len(b.data) - b.limit; over > 0 {
		b.data = append(b.data[:0], b.data[over:]...)
		b.start += int64(over)
	}
	return len(p), nil
}

// read returns the data available from offset, the offset to continue from, and whether data
// between offset and the buffer start was dropped. Unless final is set, an incomplete trailing
// UTF-8 sequence is held back, so that a multi-byte character is never split between two reads.
func (b *outputBuffer) read(offset int64, final bool) ([]byte, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	end := b.start + int64(len(b.data))
	truncated := false
	if offset < b.start {
		offset = b.start
		truncated = true
	}
	if offset > end {
		offset = end
	}

	chunk := b.data[offset-b.start:]
	if truncated {
		// dropping old data may have cut a character in half
		for i := 0; i < utf8.UTFMax-1 && len(chunk) > 0 && !utf8.RuneStart(chunk[0]); i++ {
			chunk = chunk[1:]
			offset++
		}
	}
	if !final {
		chunk = chunk[:len(chunk)-incompleteRuneTail(chunk)]
	}

	out := make([]byte, len(chunk))
	copy(out, chunk)
	return out, offset + int64(len(out)), truncated
}

// incompleteRuneTail returns the length of an incomplete UTF-8 sequence at the end of b
func incompleteRuneTail(b []byte) int {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if utf8.FullRune(b[len(b)-i:]) {
				return 0
			}
			return i
		}
	}
	return 0
}

// job is a command running in the proxy container
type job struct {
	id      string
	command string
	cwd     string
	env     []string
	pidFile string

	stdout *outputBuffer
	stderr *outputBuffer

	ctx    context.Context // cancelled to detach from the exec stream
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	containerID string // set once the process has been started
	killSignal  string // strongest signal requested so far
	exited      bool
	exitCode    int
	finishedAt  time.Time
	infraErr    error // the command could not be run at all (container unavailable etc.)
	lifetime    *time.Timer
}

func newJob(parent context.Context, command, cwd string, env []string, outputLimit int) *job {
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(parent)
	return &job{
		id:      id,
		command: command,
		cwd:     cwd,
		env:     env,
		pidFile: path.Join(jobPidDir, id+".pid"),
		stdout:  newOutputBuffer(outputLimit),
		stderr:  newOutputBuffer(outputLimit),
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
}

// state returns whether the job has exited and its exit code
func (j *job) state() (bool, int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.exited, j.exitCode
}

func (j *job) finish(exitCode int, infraErr error) {
	j.mu.Lock()
	if j.exited {
		j.mu.Unlock()
		return
	}
	j.exited = true
	j.exitCode = exitCode
	j.infraErr = infraErr
	j.finishedAt = time.Now()
	if j.lifetime != nil {
		j.lifetime.Stop()
	}
	j.mu.Unlock()

	j.cancel()
	close(j.done)
}

// requestKill remembers the requested signal. Returns the container the process runs in,
// or "" if it has not been started yet (it then will not be started at all).
func (j *job) requestKill(signal string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.killSignal != "KILL" {
		j.killSignal = signal
	}
	return j.containerID
}

// markStarted records the container the process is about to be started in.
// Returns false if the job was killed before it could start.
func (j *job) markStarted(containerID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.killSignal != "" {
		return false
	}
	j.containerID = containerID
	return true
}

func (j *job) pendingKillExitCode() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.killSignal == "KILL" {
		return 128 + 9
	}
	return 128 + 15
}

// jobPoll is the response of wait/peek
type jobPoll struct {
	JobID        string `json:"job_id"`
	Status       string `json:"status"`
	ExitCode     *int   `json:"exit_code"`
	StdoutDelta  string `json:"stdout_delta"`
	StdoutOffset int64  `json:"stdout_offset"`
	StderrDelta  string `json:"stderr_delta"`
	StderrOffset int64  `json:"stderr_offset"`
	Truncated    bool   `json:"truncated"`
}

func (j *job) poll(stdoutOffset, stderrOffset int64) jobPoll {
	// Status is taken before the output: once exited, no more output can arrive,
	// so a response saying "exited" always carries everything up to the end.
	exited, exitCode := j.state()

	stdout, stdoutOffset, stdoutTruncated := j.stdout.read(stdoutOffset, exited)
	stderr, stderrOffset, stderrTruncated := j.stderr.read(stderrOffset, exited)

	resp := jobPoll{
		JobID:        j.id,
		Status:       "running",
		StdoutDelta:  string(stdout),
		StdoutOffset: stdoutOffset,
		StderrDelta:  string(stderr),
		StderrOffset: stderrOffset,
		Truncated:    stdoutTruncated || stderrTruncated,
	}
	if exited {
		resp.Status = "exited"
		resp.ExitCode = &exitCode
	}
	return resp
}

// launchJob starts the job in the background. The returned job is finished when its process exits.
func (p *Proxy) launchJob(j *job, maxLifetime time.Duration) {
	if maxLifetime > 0 {
		j.mu.Lock()
		j.lifetime = time.AfterFunc(maxLifetime, func() {
			p.logger.Warn("job exceeded max_lifetime, killing", zap.String("jobID", j.id), zap.Duration("maxLifetime", maxLifetime))
			p.forceKill(j)
		})
		j.mu.Unlock()
	}
	go p.runJob(j)
}

func (p *Proxy) runJob(j *job) {
	containerID, err := p.readyContainer(j.ctx)
	if err != nil {
		p.logger.Error("job: container is not available", zap.String("jobID", j.id), zap.Error(err))
		fmt.Fprintf(j.stderr, "[command-proxy] container is not available: %v\n", err)
		j.finish(-1, err)
		return
	}

	if !j.markStarted(containerID) {
		j.finish(j.pendingKillExitCode(), nil)
		return
	}

	p.logger.Info("job started", zap.String("jobID", j.id), zap.String("command", j.command))
	exitCode, err := p.dockerClient.ExecStream(j.ctx, containerID, docker.ExecStreamOptions{
		Cmd:    []string{"sh", "-c", jobWrapperScript, "sh", j.command, j.pidFile, j.cwd},
		Env:    j.env,
		Stdout: j.stdout,
		Stderr: j.stderr,
	})
	_ = p.updateLastActive(time.Now())

	if err != nil {
		if j.ctx.Err() != nil {
			// we detached from the stream ourselves after a forced kill
			j.finish(128+9, nil)
			return
		}
		p.logger.Error("job: exec failed", zap.String("jobID", j.id), zap.Error(err))
		fmt.Fprintf(j.stderr, "[command-proxy] exec failed: %v\n", err)
		j.finish(-1, err)
		return
	}

	p.logger.Info("job exited", zap.String("jobID", j.id), zap.Int("exitCode", exitCode))
	j.finish(exitCode, nil)
}

// signalJob sends signal ("TERM" or "KILL") to the job's process tree.
// Returns false if the process was not running (it has not been started or has already exited).
func (p *Proxy) signalJob(ctx context.Context, j *job, signal string) bool {
	containerID := j.requestKill(signal)
	if containerID == "" {
		// not started yet; runJob will not start it
		return true
	}

	_, stderr, exitCode, err := p.dockerClient.ExecInContainer(ctx, containerID, []string{"sh", "-c", jobKillScript, "sh", j.pidFile, signal}, 4)
	if err != nil {
		p.logger.Error("job: failed to deliver signal", zap.String("jobID", j.id), zap.String("signal", signal), zap.Error(err), zap.String("stderr", stderr))
		return true
	}
	return exitCode != 3
}

// forceKill SIGKILLs the job and makes sure it gets finished even if its output stream stays open
// (e.g. a daemonized grandchild that escaped the process tree still holds stdout).
func (p *Proxy) forceKill(j *job) {
	ctx, cancel := context.WithTimeout(context.Background(), jobKillGracePeriod)
	defer cancel()
	p.signalJob(ctx, j, "KILL")

	select {
	case <-j.done:
	case <-time.After(jobKillGracePeriod):
		p.logger.Warn("job output stream still open after SIGKILL, detaching", zap.String("jobID", j.id))
		j.cancel()
	}
}

// registerJob adds a background job, enforcing the concurrency limit
func (p *Proxy) registerJob(j *job) bool {
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()

	running := 0
	for _, other := range p.jobs {
		if exited, _ := other.state(); !exited {
			running++
		}
	}
	if running >= p.maxJobs {
		return false
	}
	p.jobs[j.id] = j
	return true
}

func (p *Proxy) getJob(id string) *job {
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()
	return p.jobs[id]
}

// hasRunningJobs reports whether any command (a background job or a synchronous exec) is running
func (p *Proxy) hasRunningJobs() bool {
	if p.execs.Load() > 0 {
		return true
	}
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()
	for _, j := range p.jobs {
		if exited, _ := j.state(); !exited {
			return true
		}
	}
	return false
}

// collectJobs forgets jobs that finished longer than jobRetention ago
func (p *Proxy) collectJobs(now time.Time) {
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()
	for id, j := range p.jobs {
		j.mu.Lock()
		expired := j.exited && now.Sub(j.finishedAt) > jobRetention
		j.mu.Unlock()
		if expired {
			delete(p.jobs, id)
		}
	}
}
