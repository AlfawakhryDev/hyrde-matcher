// Package match decides which engineers should be offered a milestone.
//
// It is built for the hot path: a request is answered from an immutable
// in-memory snapshot, so ranking never waits on a database or the network.
// The snapshot is rebuilt in the background (see Refresher) and swapped in
// atomically, the same shape a real-time bidder uses for its campaign index.
package match

import "strings"

// Engineer is one row of the engineers table: the profile Hyrde approves plus
// the track record the stats worker keeps current from milestone events.
type Engineer struct {
	ID            string   `dynamodbav:"engineer_id" json:"id"`
	Name          string   `dynamodbav:"name" json:"name"`
	Skills        []string `dynamodbav:"skills" json:"skills"`
	Status        string   `dynamodbav:"status" json:"status"` // only "approved" is matched
	HourlyRateUSD int      `dynamodbav:"hourly_rate_usd" json:"hourlyRateUsd"`
	MaxConcurrent int      `dynamodbav:"max_concurrent" json:"maxConcurrent"`

	// Track record, written by the stats worker with atomic counters.
	Active    int `dynamodbav:"active" json:"active"`       // milestones in progress
	Delivered int `dynamodbav:"delivered" json:"delivered"` // milestones handed in
	OnTime    int `dynamodbav:"on_time" json:"onTime"`      // ...of which before the deadline
	Reviewed  int `dynamodbav:"reviewed" json:"reviewed"`   // milestones a delivery lead reviewed
	Approved  int `dynamodbav:"approved" json:"approved"`   // ...of which approved first time
}

func normalize(skill string) string { return strings.ToLower(strings.TrimSpace(skill)) }

// laplace smooths a rate so a newcomer with no history scores 0.5 rather than
// 0 or 1: one success and one failure are assumed before any real data.
func laplace(hits, total int) float64 { return float64(hits+1) / float64(total+2) }

// Reliability blends on-time delivery and first-time approval.
func (e *Engineer) Reliability() float64 {
	return (laplace(e.OnTime, e.Delivered) + laplace(e.Approved, e.Reviewed)) / 2
}

// Headroom is the share of capacity still free, 0 when fully booked.
func (e *Engineer) Headroom() float64 {
	if e.MaxConcurrent <= 0 {
		return 0
	}
	return max(0, 1-float64(e.Active)/float64(e.MaxConcurrent))
}
