package docker

import "context"

type AuthConfig struct {
	Server   string
	Username string
	Password string
}

type ContainerConfig struct {
	Image string
	Env   map[string]string
}

type ContainerInspect struct {
	ID        string
	Image     string
	State     string
	CreatedAt string
}

type DockerClient interface {
	PullImage(ctx context.Context, image string, auth AuthConfig) error
	CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	ListRunnedContainers(ctx context.Context) ([]ContainerInspect, error)
	InspectContainer(ctx context.Context, id string) (ContainerInspect, error)
	Close() error
}
