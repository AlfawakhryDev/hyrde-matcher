package api

import "github.com/prometheus/client_golang/prometheus"

// Metrics are registered once per process and scraped at /metrics.
type Metrics struct {
	MatchLatency   prometheus.Histogram
	Matches        *prometheus.CounterVec
	EventsAccepted prometheus.Counter
	IndexSize      prometheus.Gauge
	IndexAge       prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		MatchLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "hyrde_match_duration_seconds",
			Help: "Time to rank engineers for one milestone, handler in to response out.",
			// Fine buckets below a millisecond: that is where this service lives.
			Buckets: []float64{.0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1},
		}),
		Matches:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hyrde_matches_total", Help: "Match requests by outcome."}, []string{"outcome"}),
		EventsAccepted: prometheus.NewCounter(prometheus.CounterOpts{Name: "hyrde_events_accepted_total", Help: "Lifecycle events accepted onto Kafka."}),
		IndexSize:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "hyrde_index_engineers", Help: "Engineers available for matching."}),
		IndexAge:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "hyrde_index_age_seconds", Help: "Age of the snapshot being served."}),
	}
	reg.MustRegister(m.MatchLatency, m.Matches, m.EventsAccepted, m.IndexSize, m.IndexAge)
	return m
}
