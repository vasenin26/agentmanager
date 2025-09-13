package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/yourorg/agent-svc/internal/docker"
	"github.com/yourorg/agent-svc/internal/models"
	"github.com/yourorg/agent-svc/internal/service"
	"github.com/yourorg/agent-svc/internal/store"
)

type mockDocker struct{}
func (m *mockDocker) PullImage(ctx context.Context, image string, auth docker.AuthConfig) error { return nil }
func (m *mockDocker) CreateContainer(ctx context.Context, cfg docker.ContainerConfig) (string, error) { return "test-1", nil }
func (m *mockDocker) StartContainer(ctx context.Context, id string) error { return nil }
func (m *mockDocker) StopContainer(ctx context.Context, id string) error { return nil }
func (m *mockDocker) InspectContainer(ctx context.Context, id string) (docker.ContainerInspect, error) { return docker.ContainerInspect{ID:id,Image:"alpine",State:"running"}, nil }

func TestCreateAndStart(t *testing.T) {
	dc := &mockDocker{}
	st := store.NewMemoryStore()
	svc := service.NewAgentService(dc, st, docker.AuthConfig{}, 5*time.Second)
	req := models.CreateAgentRequest{Image: "alpine:latest"}
	ai, err := svc.CreateAndStart(context.Background(), req)
	if err != nil { t.Fatalf("expected no error, got %v", err) }
	if ai.ID == "" { t.Fatalf("expected id") }
}
