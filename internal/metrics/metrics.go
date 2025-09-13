package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	CreatedAgents = prometheus.NewCounter(prometheus.CounterOpts{ Name: "agents_created_total", Help: "Total created agents" })
	AgentErrors = prometheus.NewCounter(prometheus.CounterOpts{ Name: "agents_errors_total", Help: "Total agent errors" })
)

func Register() {
	prometheus.MustRegister(CreatedAgents, AgentErrors)
}
