package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vasenin26/agentmanager/internal/docker"
)

var (
	CreatedAgents = prometheus.NewCounter(prometheus.CounterOpts{Name: "agents_created_total", Help: "Total created agents"})
	AgentErrors   = prometheus.NewCounter(prometheus.CounterOpts{Name: "agents_errors_total", Help: "Total agent errors"})

	// Новые метрики
	ContainerStartCommands = prometheus.NewCounter(prometheus.CounterOpts{Name: "container_start_commands_total", Help: "Total container start commands"})
	ContainerStopCommands  = prometheus.NewCounter(prometheus.CounterOpts{Name: "container_stop_commands_total", Help: "Total container stop commands"})
	ProcessStartCommands   = prometheus.NewCounter(prometheus.CounterOpts{Name: "process_start_commands_total", Help: "Total process start commands"})
)

// RunningContainersCollector - кастомный коллектор для подсчета запущенных контейнеров
type RunningContainersCollector struct {
	dockerClient docker.DockerClient
}

// NewRunningContainersCollector создает новый коллектор
func NewRunningContainersCollector(dc docker.DockerClient) *RunningContainersCollector {
	return &RunningContainersCollector{dockerClient: dc}
}

// Describe реализует prometheus.Collector
func (c *RunningContainersCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- prometheus.NewDesc("containers_running_total", "Number of currently running containers", nil, nil)
}

// Collect реализует prometheus.Collector
func (c *RunningContainersCollector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	containers, err := c.dockerClient.ListRunnedContainers(ctx)
	if err != nil {
		// В случае ошибки возвращаем 0
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc("containers_running_total", "Number of currently running containers", nil, nil),
			prometheus.GaugeValue,
			0,
		)
		return
	}

	runningCount := 0
	for _, container := range containers {
		if container.State == "running" {
			runningCount++
		}
	}

	ch <- prometheus.MustNewConstMetric(
		prometheus.NewDesc("containers_running_total", "Number of currently running containers", nil, nil),
		prometheus.GaugeValue,
		float64(runningCount),
	)
}

func Register() {
	prometheus.MustRegister(CreatedAgents, AgentErrors, ContainerStartCommands, ContainerStopCommands, ProcessStartCommands)
}

// RegisterWithDockerClient регистрирует метрики с Docker клиентом
func RegisterWithDockerClient(dc docker.DockerClient) {
	prometheus.MustRegister(CreatedAgents, AgentErrors, ContainerStartCommands, ContainerStopCommands, ProcessStartCommands, NewRunningContainersCollector(dc))
}
