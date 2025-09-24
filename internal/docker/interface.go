package docker

import "context"

// AuthConfig содержит параметры аутентификации для приватного реестра.
type AuthConfig struct {
	Server   string
	Username string
	Password string
}

// ContainerConfig описывает параметры контейнера.
// Env будет передан Docker Engine в формате KEY=VALUE.
// Доп. параметры (Cmd/Entrypoint/Volumes) могут быть добавлены при необходимости.
type ContainerConfig struct {
	Image string
	Env   map[string]string
}

// ContainerInspect — результат инспекции контейнера.
// CreatedAt — фактическая дата/время создания из Docker Engine (ISO8601).
type ContainerInspect struct {
	ID        string
	Image     string
	State     string
	CreatedAt string
}

// DockerClient описывает операции с Docker Engine.
// CreateContainer создаёт и сразу запускает контейнер; возвращает ID запущенного контейнера.
// PullImage тянет образ (с поддержкой RegistryAuth).
// StartContainer/StopContainer — идемпотентны относительно текущего состояния.
type DockerClient interface {
	PullImage(ctx context.Context, image string, auth AuthConfig) error
	CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error)
	StartContainer(ctx context.Context, id string) error
	StopContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, id string) (ContainerInspect, error)
}
