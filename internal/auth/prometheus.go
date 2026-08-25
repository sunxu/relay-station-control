package auth

import "github.com/prometheus/client_golang/prometheus"

type PrometheusCollector struct {
	metrics *AuthMetrics
	descs   map[string]*prometheus.Desc
}

func NewPrometheusCollector(metrics *AuthMetrics) *PrometheusCollector {
	if metrics == nil {
		metrics = NewAuthMetrics()
	}
	descs := make(map[string]*prometheus.Desc, len(metricDescriptors))
	for _, descriptor := range metricDescriptors {
		descs[descriptor.Name] = prometheus.NewDesc(descriptor.Name, "Relay Station Control authentication metric.", descriptor.Labels, nil)
	}
	return &PrometheusCollector{metrics: metrics, descs: descs}
}

func (c *PrometheusCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, descriptor := range metricDescriptors {
		ch <- c.descs[descriptor.Name]
	}
}

func (c *PrometheusCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sample := range c.metrics.Snapshot() {
		descriptor, ok := c.descs[sample.Name]
		if !ok {
			continue
		}
		definition := metricDescriptor(sample.Name)
		labels := make([]string, 0, len(definition.Labels))
		for _, name := range definition.Labels {
			labels = append(labels, sample.Labels[name])
		}
		valueType := prometheus.CounterValue
		if sample.Name == "relay_control_auth_active_sessions" {
			valueType = prometheus.GaugeValue
		}
		ch <- prometheus.MustNewConstMetric(descriptor, valueType, sample.Value, labels...)
	}
}

func metricDescriptor(name string) MetricDescriptor {
	for _, descriptor := range metricDescriptors {
		if descriptor.Name == name {
			return descriptor
		}
	}
	return MetricDescriptor{}
}
