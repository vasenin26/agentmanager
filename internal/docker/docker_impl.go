package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	docker "github.com/fsouza/go-dockerclient"
	"go.uber.org/zap"
)

// realDocker - реальная имплементация Docker клиента
type realDocker struct {
	client *docker.Client
	logger *zap.Logger
}

// New создает реальную реализацию Docker клиента
func New(logger *zap.Logger) (DockerClient, error) {
	cli, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &realDocker{
		client: cli,
		logger: logger,
	}, nil
}

// Реализация методов для realDocker

func (r *realDocker) PullImage(ctx context.Context, image string, auth AuthConfig) error {
	// Allow skipping image pull for local development
	if v := strings.ToLower(os.Getenv("AGENT_IMAGE_PULL")); v == "false" || v == "0" {
		r.logger.Info("Skipping image pull due to AGENT_IMAGE_PULL flag", zap.String("image", image))
		return nil
	}

	// If image already exists locally, skip pulling
	if img, err := r.client.InspectImage(image); err == nil && img != nil {
		r.logger.Info("Image exists locally, skipping pull", zap.String("image", image))
		return nil
	}

	r.logger.Info("Pulling image",
		zap.String("image", image),
		zap.String("registry", auth.Server),
		zap.String("username", auth.Username))

	// Разбираем имя образа на репозиторий и тег
	repository := image
	tag := "latest"
	if at := strings.Index(image, "@"); at != -1 { // образ по digest
		repository = image[:at]
	}
	if slash := strings.LastIndex(repository, "/"); true {
		_ = slash
	}
	if colon := strings.LastIndex(repository, ":"); colon != -1 && colon > strings.LastIndex(repository, "/") {
		tag = repository[colon+1:]
		repository = repository[:colon]
	}

	opts := docker.PullImageOptions{
		Repository:   repository,
		Tag:          tag,
		OutputStream: io.Discard,
	}
	authCfg := docker.AuthConfiguration{
		ServerAddress: auth.Server,
		Username:      auth.Username,
		Password:      auth.Password,
	}

	if err := r.client.PullImage(opts, authCfg); err != nil {
		r.logger.Error("Failed to pull image", zap.String("image", image), zap.Error(err))
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}

	r.logger.Info("Successfully pulled image", zap.String("image", image))
	return nil
}

func (r *realDocker) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	r.logger.Info("Creating container", zap.String("image", cfg.Image))

	// Подготавливаем переменные окружения
	var env []string
	for key, value := range cfg.Env {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// Подготавливаем binds для volumes
	var binds []string
	for _, vol := range cfg.Volumes {
		binds = append(binds, fmt.Sprintf("%s:%s", vol.VolumeID, vol.MountPath))
	}

	containerConfig := &docker.Config{
		Image: cfg.Image,
		Env:   env,
	}
	hostConfig := &docker.HostConfig{
		AutoRemove: true,
		Memory:     cfg.MemoryLimit,
		Binds:      binds,
	}

	created, err := r.client.CreateContainer(docker.CreateContainerOptions{
		Config:     containerConfig,
		HostConfig: hostConfig,
		Name:       "",
	})
	if err != nil {
		r.logger.Error("Failed to create container", zap.String("image", cfg.Image), zap.Error(err))
		return "", fmt.Errorf("failed to create container for image %s: %w", cfg.Image, err)
	}

	r.logger.Info("Successfully created container",
		zap.String("containerID", created.ID),
		zap.String("image", cfg.Image))

	return created.ID, nil
}

func (r *realDocker) StartContainer(ctx context.Context, id string) error {
	r.logger.Info("Starting container", zap.String("containerID", id))

	err := r.client.StartContainer(id, nil)
	if err != nil {
		r.logger.Error("Failed to start container", zap.String("containerID", id), zap.Error(err))
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}

	r.logger.Info("Successfully started container", zap.String("containerID", id))
	return nil
}

func (r *realDocker) StopContainer(ctx context.Context, id string) error {
	r.logger.Info("Stopping container", zap.String("containerID", id))

	var timeout uint = 30 // 30 секунд на graceful shutdown
	err := r.client.StopContainer(id, timeout)
	if err != nil {
		r.logger.Error("Failed to stop container", zap.String("containerID", id), zap.Error(err))
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}

	r.logger.Info("Successfully stopped container", zap.String("containerID", id))
	return nil
}

func (r *realDocker) InspectContainer(ctx context.Context, id string) (ContainerInspect, error) {
	r.logger.Debug("Inspecting container", zap.String("containerID", id))

	containerInfo, err := r.client.InspectContainer(id)
	if err != nil {
		r.logger.Error("Failed to inspect container", zap.String("containerID", id), zap.Error(err))
		return ContainerInspect{}, fmt.Errorf("failed to inspect container %s: %w", id, err)
	}

	// Преобразуем состояние контейнера в строку
	var state string
	if containerInfo.State.Running {
		state = "running"
	} else if containerInfo.State.ExitCode != 0 {
		state = "exited"
	} else {
		state = "created"
	}

	// Форматируем время создания
	createdAt := fmt.Sprintf("%v", containerInfo.Created)

	result := ContainerInspect{
		ID:        containerInfo.ID,
		Image:     containerInfo.Config.Image,
		State:     state,
		CreatedAt: createdAt,
	}

	r.logger.Debug("Successfully inspected container",
		zap.String("containerID", id),
		zap.String("state", state))

	return result, nil
}

func (r *realDocker) Close() error {
	r.logger.Info("Closing docker client")
	if r.client != nil {
		// fsouza/go-dockerclient does not require explicit close
		return nil
	}
	return nil
}

func (r *realDocker) ListRunnedContainers(ctx context.Context) ([]ContainerInspect, error) {
	r.logger.Info("Listing runned containers")

	containers, err := r.client.ListContainers(docker.ListContainersOptions{All: true})
	if err != nil {
		r.logger.Error("Failed to list runned containers", zap.Error(err))
		return nil, fmt.Errorf("failed to list runned containers: %w", err)
	}

	result := make([]ContainerInspect, 0, len(containers))
	for _, container := range containers {
		result = append(result, ContainerInspect{
			ID:        container.ID,
			Image:     container.Image,
			State:     container.State,
			CreatedAt: fmt.Sprintf("%v", container.Created),
		})
	}

	return result, nil
}

func (r *realDocker) CreateVolume(ctx context.Context, name string) (string, error) {
	r.logger.Info("Creating volume", zap.String("name", name))

	volume, err := r.client.CreateVolume(docker.CreateVolumeOptions{
		Name:   name,
		Driver: "local",
	})
	if err != nil {
		r.logger.Error("Failed to create volume", zap.String("name", name), zap.Error(err))
		return "", fmt.Errorf("failed to create volume: %w", err)
	}

	r.logger.Info("Successfully created volume", zap.String("volumeID", volume.Name))
	return volume.Name, nil
}

func (r *realDocker) DeleteVolume(ctx context.Context, volumeID string) error {
	r.logger.Info("Deleting volume", zap.String("volumeID", volumeID))

	err := r.client.RemoveVolume(volumeID)
	if err != nil {
		r.logger.Error("Failed to delete volume", zap.String("volumeID", volumeID), zap.Error(err))
		return fmt.Errorf("failed to delete volume: %w", err)
	}

	r.logger.Info("Successfully deleted volume", zap.String("volumeID", volumeID))
	return nil
}

func (r *realDocker) ListenEvents(ctx context.Context, eventChan chan<- DockerEvent) error {
	r.logger.Info("Starting Docker event listener")

	listener := make(chan *docker.APIEvents)

	err := r.client.AddEventListener(listener)
	if err != nil {
		r.logger.Error("Failed to add event listener", zap.Error(err))
		return fmt.Errorf("failed to add event listener: %w", err)
	}

	go func() {
		defer r.client.RemoveEventListener(listener)
		for {
			select {
			case event := <-listener:
				if event.Type == "container" && (event.Status == "die" || event.Status == "stop") {
					exitCode := 0
					if exitCodeStr, ok := event.Actor.Attributes["exitCode"]; ok {
						fmt.Sscanf(exitCodeStr, "%d", &exitCode)
					}
					eventChan <- DockerEvent{
						ContainerID: event.Actor.ID,
						Status:      event.Status,
						ExitCode:    exitCode,
					}
				}
			case <-ctx.Done():
				r.logger.Info("Stopping Docker event listener")
				return
			}
		}
	}()

	return nil
}

func (r *realDocker) GetSystemMemory(ctx context.Context) (int64, error) {
	r.logger.Debug("Getting system memory")

	info, err := r.client.Info()
	if err != nil {
		r.logger.Error("Failed to get system info", zap.Error(err))
		return 0, fmt.Errorf("failed to get system info: %w", err)
	}

	return info.MemTotal, nil
}
