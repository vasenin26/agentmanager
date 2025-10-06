package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vasenin26/agentmanager/internal/docker"
)

var (
	// Orchestrator metrics
	TasksFetchedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orchestrator_tasks_fetched_total",
		Help: "Total number of tasks fetched from external API",
	})

	TasksProcessedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orchestrator_tasks_processed_total",
		Help: "Total number of tasks processed successfully",
	})

	TasksFailedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orchestrator_tasks_failed_total",
		Help: "Total number of tasks that failed",
	})

	ContextsCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orchestrator_contexts_created_total",
		Help: "Total number of contexts created",
	})

	ContextsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orchestrator_contexts_active",
		Help: "Number of currently active contexts",
	})

	ContextQueueLength = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "orchestrator_context_queue_length",
		Help: "Length of task queue for each context",
	}, []string{"context_id"})

	AvailableMemoryBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orchestrator_available_memory_bytes",
		Help: "Available memory in bytes",
	})

	UsedMemoryBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orchestrator_used_memory_bytes",
		Help: "Used memory by agents in bytes",
	})

	ActiveAgents = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orchestrator_active_agents",
		Help: "Number of currently active agents",
	})

	// Task reservation metrics
	TaskReservationConflictsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orchestrator_task_reservation_conflicts_total",
		Help: "Total number of task reservation conflicts (409 Conflict responses)",
	})
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
	prometheus.MustRegister(
		TasksFetchedTotal,
		TasksProcessedTotal,
		TasksFailedTotal,
		ContextsCreatedTotal,
		ContextsActive,
		ContextQueueLength,
		AvailableMemoryBytes,
		UsedMemoryBytes,
		ActiveAgents,
		TaskReservationConflictsTotal,
	)
}

// RegisterWithDockerClient регистрирует метрики с Docker клиентом
func RegisterWithDockerClient(dc docker.DockerClient) {
	prometheus.MustRegister(
		TasksFetchedTotal,
		TasksProcessedTotal,
		TasksFailedTotal,
		ContextsCreatedTotal,
		ContextsActive,
		ContextQueueLength,
		AvailableMemoryBytes,
		UsedMemoryBytes,
		ActiveAgents,
		TaskReservationConflictsTotal,
		NewRunningContainersCollector(dc),
	)
}
