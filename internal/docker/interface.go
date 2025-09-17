package docker

import "context"

// AuthConfig описывает параметры авторизации для Docker Registry.
// Используется при PullImage; если поля пустые — аутентификация не выполняется.
type AuthConfig struct {
	Server   string
	Username string
	Password string
}

// ContainerConfig описывает параметры создаваемого контейнера.
// Env будет передан Docker Engine в формате KEY=VALUE и применён в контейнере.
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
