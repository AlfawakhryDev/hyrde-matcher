// Package events defines the milestone lifecycle events that flow through
// Kafka, and what each one does to an engineer's track record.
package events

import (
	"errors"
	"time"
)

// Type is the kind of event. Consumers switch on it.
type Type string

const (
	MilestoneAssigned  Type = "milestone.assigned"  // an engineer took the milestone
	MilestoneDelivered Type = "milestone.delivered" // they handed it in
	MilestoneReviewed  Type = "milestone.reviewed"  // a delivery lead reviewed it
	MatchServed        Type = "match.served"        // the matcher answered a request (audit trail)
)

// Event is one fact about a milestone. ID is the idempotency key: Kafka
// delivers at least once, so the same event may arrive twice and must only
// count once.
type Event struct {
	ID          string    `json:"id"`
	Type        Type      `json:"type"`
	MilestoneID string    `json:"milestoneId"`
	EngineerID  string    `json:"engineerId,omitempty"`
	OnTime      *bool     `json:"onTime,omitempty"`   // delivered: before the deadline?
	Approved    *bool     `json:"approved,omitempty"` // reviewed: approved first time?
	Candidates  []string  `json:"candidates,omitempty"`
	At          time.Time `json:"at"`
}

// Validate rejects events a consumer could not apply. Returning an error
// instead of panicking: in Go, failure is a value the caller decides about.
func (e Event) Validate() error {
	switch {
	case e.ID == "":
		return errors.New("id is required")
	case e.MilestoneID == "":
		return errors.New("milestoneId is required")
	}
	switch e.Type {
	case MilestoneAssigned:
	case MilestoneDelivered:
		if e.OnTime == nil {
			return errors.New("delivered events need onTime")
		}
	case MilestoneReviewed:
		if e.Approved == nil {
			return errors.New("reviewed events need approved")
		}
	case MatchServed:
		return nil // no engineer: it is about a request, not a person
	default:
		return errors.New("unknown event type")
	}
	if e.EngineerID == "" {
		return errors.New("engineerId is required")
	}
	return nil
}

// Delta is what an event adds to an engineer's counters.
type Delta struct {
	Active, Delivered, OnTime, Reviewed, Approved int
}

// Delta maps an event to counter changes. ok is false for events that do not
// touch an engineer's record.
func (e Event) Delta() (d Delta, ok bool) {
	switch e.Type {
	case MilestoneAssigned:
		return Delta{Active: 1}, true
	case MilestoneDelivered:
		d = Delta{Active: -1, Delivered: 1}
		if *e.OnTime {
			d.OnTime = 1
		}
		return d, true
	case MilestoneReviewed:
		d = Delta{Reviewed: 1}
		if *e.Approved {
			d.Approved = 1
		}
		return d, true
	}
	return Delta{}, false
}
