package docker

import (
	"context"
	"fmt"
	"sync"
)

type fakeDocker struct {
	mu sync.Mutex
	containers map[string]ContainerInspect
}

func NewFake() DockerClient {
	return &fakeDocker{containers: map[string]ContainerInspect{}}
}

func (f *fakeDocker) PullImage(ctx context.Context, image string, auth AuthConfig) error { return nil }
func (f *fakeDocker) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	id := fmt.Sprintf("fake-%d", len(f.containers)+1)
	f.containers[id] = ContainerInspect{ID: id, Image: cfg.Image, State: "created", CreatedAt: "now"}
	return id, nil
}
func (f *fakeDocker) StartContainer(ctx context.Context, id string) error {
	f.mu.Lock(); defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok { return fmt.Errorf("container not found") }
	c.State = "running"
	f.containers[id] = c
	return nil
}
func (f *fakeDocker) StopContainer(ctx context.Context, id string) error {
	f.mu.Lock(); defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok { return fmt.Errorf("container not found") }
	c.State = "exited"
	f.containers[id] = c
	return nil
}
func (f *fakeDocker) InspectContainer(ctx context.Context, id string) (ContainerInspect, error) {
	f.mu.Lock(); defer f.mu.Unlock()
	c, ok := f.containers[id]
	if !ok { return ContainerInspect{}, fmt.Errorf("container not found") }
	return c, nil
}
