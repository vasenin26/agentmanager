//go:build docker_e2e

package docker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDockerE2E(t *testing.T) {
	ctx := context.Background()
	dc := New()
	require.NoError(t, dc.PullImage(ctx, "alpine:3", AuthConfig{}))
	id, err := dc.CreateContainer(ctx, ContainerConfig{Image: "alpine:3", Env: map[string]string{"FOO": "BAR"}})
	require.NoError(t, err)
	// Give container a moment to be visible/running
	time.Sleep(500 * time.Millisecond)
	insp, err := dc.InspectContainer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "running", insp.State)
	require.Contains(t, insp.Image, "alpine:3")
	require.NotEmpty(t, insp.CreatedAt)
	require.NoError(t, dc.StartContainer(ctx, id))
	require.NoError(t, dc.StopContainer(ctx, id))
	// Give some time for stop
	time.Sleep(500 * time.Millisecond)
	insp, _ = dc.InspectContainer(ctx, id)
	require.Equal(t, "exited", insp.State)
	require.NoError(t, dc.StartContainer(ctx, id))
	// Give time to restart
	time.Sleep(500 * time.Millisecond)
	insp, _ = dc.InspectContainer(ctx, id)
	require.Equal(t, "running", insp.State)
}
