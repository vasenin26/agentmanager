package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
)

type dockerClient struct {
	cli *client.Client
}

// New возвращает реализацию DockerClient поверх Docker Engine API.
func New() DockerClient {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(fmt.Errorf("failed to init docker client: %w", err))
	}
	return &dockerClient{cli: cli}
}

func (d *dockerClient) PullImage(ctx context.Context, image string, auth AuthConfig) error {
	var authStr string
	if auth.Server != "" || auth.Username != "" || auth.Password != "" {
		ac := registry.AuthConfig{ServerAddress: auth.Server, Username: auth.Username, Password: auth.Password}
		b, err := json.Marshal(ac)
		if err != nil {
			return fmt.Errorf("marshal auth: %w", err)
		}
		authStr = base64.URLEncoding.EncodeToString(b)
	}

	rc, err := d.cli.ImagePull(ctx, image, types.ImagePullOptions{RegistryAuth: authStr})
	if err != nil {
		return fmt.Errorf("image pull: %w", err)
	}
	defer rc.Close()
	// прочитаем поток до конца, чтобы Docker завершил операцию
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

func (d *dockerClient) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	env := make([]string, 0, len(cfg.Env))
	for k, v := range cfg.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	contCfg := &container.Config{
		Image: cfg.Image,
		Env:   env,
	}

	resp, err := d.cli.ContainerCreate(ctx, contCfg, &container.HostConfig{}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("container start: %w", err)
	}

	return resp.ID, nil
}

func (d *dockerClient) StartContainer(ctx context.Context, id string) error {
	inspect, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect before start: %w", err)
	}
	if inspect.ContainerJSONBase != nil && inspect.ContainerJSONBase.State != nil && inspect.ContainerJSONBase.State.Running {
		return nil
	}
	if err := d.cli.ContainerStart(ctx, id, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("container start: %w", err)
	}
	return nil
}

func (d *dockerClient) StopContainer(ctx context.Context, id string) error {
	inspect, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect before stop: %w", err)
	}
	running := inspect.ContainerJSONBase != nil && inspect.ContainerJSONBase.State != nil && inspect.ContainerJSONBase.State.Running
	if !running {
		return nil
	}

	timeout := 10 * time.Second
	if err := d.cli.ContainerStop(ctx, id, &timeout); err != nil {
		return fmt.Errorf("container stop: %w", err)
	}
	return nil
}

func (d *dockerClient) InspectContainer(ctx context.Context, id string) (ContainerInspect, error) {
	inspect, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return ContainerInspect{}, fmt.Errorf("container inspect: %w", err)
	}
	state := ""
	if inspect.ContainerJSONBase != nil && inspect.ContainerJSONBase.State != nil {
		state = inspect.ContainerJSONBase.State.Status
	}
	image := ""
	if inspect.Config != nil {
		image = inspect.Config.Image
	}
	created := inspect.Created
	return ContainerInspect{ID: inspect.ID, Image: image, State: state, CreatedAt: created}, nil
}
