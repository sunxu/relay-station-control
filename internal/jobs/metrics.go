package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type MetricsSnapshot struct {
	JobsByStatus         map[Status]int64
	OldestPendingSeconds float64
	ExpiredLeases        int64
	OldestOutboxSeconds  float64
}

type MetricsProvider interface {
	JobMetricsSnapshot(context.Context) (MetricsSnapshot, error)
}

type Collector struct {
	provider MetricsProvider
	jobs     *prometheus.Desc
	oldest   *prometheus.Desc
	expired  *prometheus.Desc
	outbox   *prometheus.Desc
}

func NewCollector(provider MetricsProvider) (*Collector, error) {
	if provider == nil {
		return nil, fmt.Errorf("metrics provider is nil")
	}
	return &Collector{
		provider: provider,
		jobs:     prometheus.NewDesc("relay_control_async_jobs", "Durable jobs by closed status.", []string{"status"}, nil),
		oldest:   prometheus.NewDesc("relay_control_async_job_oldest_pending_seconds", "Age of the oldest runnable durable job.", nil, nil),
		expired:  prometheus.NewDesc("relay_control_async_job_expired_leases", "Durable jobs with expired execution leases.", nil, nil),
		outbox:   prometheus.NewDesc("relay_control_outbox_oldest_pending_seconds", "Age of the oldest publishable Outbox event.", nil, nil),
	}, nil
}

func (c *Collector) Describe(channel chan<- *prometheus.Desc) {
	channel <- c.jobs
	channel <- c.oldest
	channel <- c.expired
	channel <- c.outbox
}

func (c *Collector) Collect(channel chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := c.provider.JobMetricsSnapshot(ctx)
	if err != nil {
		channel <- prometheus.NewInvalidMetric(c.jobs, fmt.Errorf("durable job metrics unavailable"))
		return
	}
	for status, count := range snapshot.JobsByStatus {
		if !status.Valid() || count < 0 {
			channel <- prometheus.NewInvalidMetric(c.jobs, fmt.Errorf("invalid durable job metrics snapshot"))
			return
		}
	}
	for _, status := range AllStatuses {
		count := snapshot.JobsByStatus[status]
		if count < 0 {
			channel <- prometheus.NewInvalidMetric(c.jobs, fmt.Errorf("invalid durable job metrics snapshot"))
			return
		}
		channel <- prometheus.MustNewConstMetric(c.jobs, prometheus.GaugeValue, float64(count), string(status))
	}
	if snapshot.OldestPendingSeconds < 0 || snapshot.ExpiredLeases < 0 || snapshot.OldestOutboxSeconds < 0 {
		channel <- prometheus.NewInvalidMetric(c.oldest, fmt.Errorf("invalid durable job metrics snapshot"))
		return
	}
	channel <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, snapshot.OldestPendingSeconds)
	channel <- prometheus.MustNewConstMetric(c.expired, prometheus.GaugeValue, float64(snapshot.ExpiredLeases))
	channel <- prometheus.MustNewConstMetric(c.outbox, prometheus.GaugeValue, snapshot.OldestOutboxSeconds)
}
