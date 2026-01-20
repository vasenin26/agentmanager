package docker

import (
	"context"
	"io"
)

type AuthConfig struct {
	Server   string
	Username string
	Password string
}

type VolumeMount struct {
	VolumeID  string
	MountPath string
}

type ContainerConfig struct {
	Image       string
	Env         map[string]string
	MemoryLimit int64         // Лимит памяти в байтах
	Volumes     []VolumeMount // Монтирование volumes
	AutoRemove  bool          // Автоматическое удаление контейнера после остановки
	Privileged  bool          // Привилегированный режим (нужен для DinD)
}

type ContainerInspect struct {
	ID        string
	Image     string
	State     string
	CreatedAt string
}

type DockerEvent struct {
	ContainerID string
	Status      string // "die", "stop", "start"
	ExitCode    int
}

// LongRunningExec represents a long-running exec process with stdin/stdout/stderr streams
type LongRunningExec struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	ExecID string
}

type DockerClient interface {
	PullImage(ctx context.Context, image string, auth AuthConfig) error
	CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error)
	// RemoveContainer forcefully removes a container
	RemoveContainer(ctx context.Context, id string) error
	// ExecInContainer executes a command inside a container and returns stdout, stderr and exit code
	ExecInContainer(ctx context.Context, id string, cmd []string, timeoutSeconds int) (string, string, int, error)
	// CreateLongRunningExec creates a long-running exec process and returns stdin/stdout/stderr streams
	CreateLongRunningExec(ctx context.Context, id string, cmd []string) (*LongRunningExec, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	ListRunnedContainers(ctx context.Context) ([]ContainerInspect, error)
	InspectContainer(ctx context.Context, id string) (ContainerInspect, error)
	CreateVolume(ctx context.Context, name string) (string, error)
	DeleteVolume(ctx context.Context, volumeID string) error
	ListenEvents(ctx context.Context, eventChan chan<- DockerEvent) error
	GetSystemMemory(ctx context.Context) (int64, error)
	GetContainerMemoryUsage(ctx context.Context, id string) (int64, error)
	Close() error
}
