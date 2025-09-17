//go:build docker_e2e

package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yourorg/agent-svc/internal/docker"
)

func TestDockerE2E(t *testing.T) {
	ctx := context.Background()
	dc := docker.New()
	require.NoError(t, dc.PullImage(ctx, "alpine:3", docker.AuthConfig{}))
	id, err := dc.CreateContainer(ctx, docker.ContainerConfig{Image: "alpine:3", Env: map[string]string{"FOO":"BAR"}})
	require.NoError(t, err)

	insp, err := dc.InspectContainer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "running", insp.State)
	require.Contains(t, insp.Image, "alpine:3")
	require.NotEmpty(t, insp.CreatedAt)

	require.NoError(t, dc.StartContainer(ctx, id))
	require.NoError(t, dc.StopContainer(ctx, id))
	insp, err = dc.InspectContainer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "exited", insp.State)

	require.NoError(t, dc.StartContainer(ctx, id))
	insp, err = dc.InspectContainer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "running", insp.State)
}
