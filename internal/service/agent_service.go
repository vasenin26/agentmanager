package service

import (
	"context"
	"time"

	"github.com/yourorg/agent-svc/internal/docker"
	"github.com/yourorg/agent-svc/internal/models"
	"github.com/yourorg/agent-svc/internal/store"
)

type AgentService struct {
	dc docker.DockerClient
	store *store.MemoryStore
	registry docker.AuthConfig
	defaultTimeout time.Duration
}

func NewAgentService(dc docker.DockerClient, s *store.MemoryStore, reg docker.AuthConfig, t time.Duration) *AgentService {
	return &AgentService{dc: dc, store: s, registry: reg, defaultTimeout: t}
}

func (as *AgentService) CreateAndStart(ctx context.Context, req models.CreateAgentRequest) (models.AgentInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, as.defaultTimeout); defer cancel()
	if err := as.dc.PullImage(ctx, req.Image, as.registry); err != nil { return models.AgentInfo{}, err }
	id, err := as.dc.CreateContainer(ctx, docker.ContainerConfig{Image: req.Image, Env: req.Env}); if err != nil { return models.AgentInfo{}, err }
	if err := as.dc.StartContainer(ctx, id); err != nil { return models.AgentInfo{}, err }
	ai := models.AgentInfo{ID: id, Image: req.Image, Status: models.StatusRunning, CreatedAt: time.Now()}
	as.store.Add(ai)
	return ai, nil
}

func (as *AgentService) Stop(ctx context.Context, id string) (models.AgentInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, as.defaultTimeout); defer cancel()
	if err := as.dc.StopContainer(ctx, id); err != nil { return models.AgentInfo{}, err }
	ai, ok := as.store.Get(id)
	if ok { ai.Status = models.StatusStopped; as.store.Update(ai); return ai, nil }
	insp, _ := as.dc.InspectContainer(ctx, id)
	return models.AgentInfo{ID: id, Image: insp.Image, Status: models.StatusStopped, CreatedAt: time.Now()}, nil
}

func (as *AgentService) ListRunning(ctx context.Context) ([]models.AgentInfo, error) {
	all := as.store.List(); res := make([]models.AgentInfo, 0)
	for _, a := range all { if a.Status == models.StatusRunning { res = append(res, a) } }
	return res, nil
}
