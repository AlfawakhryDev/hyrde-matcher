package match

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func bench() []Engineer {
	return []Engineer{
		{ID: "ana", Name: "Ana", Skills: []string{"Procore", "QuickBooks"}, Status: "approved", HourlyRateUSD: 60, MaxConcurrent: 2},
		{ID: "ben", Name: "Ben", Skills: []string{"procore"}, Status: "approved", HourlyRateUSD: 40, MaxConcurrent: 2,
			Delivered: 10, OnTime: 10, Reviewed: 10, Approved: 10},
		{ID: "cy", Name: "Cy", Skills: []string{"Procore", "QuickBooks"}, Status: "trial", HourlyRateUSD: 40, MaxConcurrent: 2},
		{ID: "dee", Name: "Dee", Skills: []string{"Procore", "QuickBooks"}, Status: "approved", HourlyRateUSD: 40, MaxConcurrent: 1, Active: 1},
		{ID: "eve", Name: "Eve", Skills: []string{"Yardi"}, Status: "approved", HourlyRateUSD: 40, MaxConcurrent: 3},
	}
}

func TestIndexKeepsOnlyAvailableApprovedEngineers(t *testing.T) {
	ix := NewIndex(bench(), time.Now())
	if ix.Len() != 3 { // cy is on trial, dee is fully booked
		t.Fatalf("Len() = %d, want 3", ix.Len())
	}
}

func TestRankPrefersFullCoverage(t *testing.T) {
	got := NewIndex(bench(), time.Now()).Rank(Request{Integrations: []string{"Procore", "QuickBooks"}})
	if len(got) != 2 || got[0].EngineerID != "ana" || got[1].EngineerID != "ben" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Coverage != 1 || got[1].Coverage != 0.5 || len(got[1].Missing) != 1 || got[1].Missing[0] != "quickbooks" {
		t.Fatalf("coverage/missing wrong: %+v", got)
	}
}

func TestRankRewardsTrackRecordAndRateFit(t *testing.T) {
	// Both cover Procore fully; Ben has a perfect record and a rate the payout
	// covers ($400 for 10h = $40/h), Ana is new and charges $60/h.
	got := NewIndex(bench(), time.Now()).Rank(Request{Integrations: []string{"procore"}, PayoutCents: 40_000, EstimatedHours: 10})
	if got[0].EngineerID != "ben" {
		t.Fatalf("want ben first, got %+v", got)
	}
	if want := 40.0 / 60.0; math.Abs(got[1].RateFit-want) > 1e-9 {
		t.Fatalf("ana rateFit = %v, want %v", got[1].RateFit, want)
	}
}

func TestRankNoIntegrationsConsidersEveryone(t *testing.T) {
	if got := NewIndex(bench(), time.Now()).Rank(Request{Limit: 10}); len(got) != 3 {
		t.Fatalf("got %d candidates, want 3", len(got))
	}
}

func TestNewcomerReliabilityIsNeutral(t *testing.T) {
	e := Engineer{}
	if e.Reliability() != 0.5 {
		t.Fatalf("Reliability() = %v, want 0.5", e.Reliability())
	}
}

// BenchmarkRank measures the hot path against a 10,000-engineer bench.
// Run: go test ./internal/match -bench Rank -benchmem
func BenchmarkRank(b *testing.B) {
	skills := []string{"Procore", "QuickBooks", "AppFolio", "Yardi", "Buildium", "Sage Intacct", "DocuSign", "Stripe", "Plaid", "Receipt OCR"}
	all := make([]Engineer, 10_000)
	for i := range all {
		all[i] = Engineer{
			ID: fmt.Sprintf("e%05d", i), Status: "approved", HourlyRateUSD: 30 + i%60, MaxConcurrent: 3, Active: i % 3,
			Skills: []string{skills[i%len(skills)], skills[(i*7)%len(skills)]}, Delivered: i % 20, OnTime: i % 15, Reviewed: i % 20, Approved: i % 17,
		}
	}
	ix := NewIndex(all, time.Now())
	req := Request{Integrations: []string{"Procore", "QuickBooks"}, PayoutCents: 200_000, EstimatedHours: 40}
	b.ReportAllocs()
	for b.Loop() { // Go 1.24+: the benchmark loop the runtime sizes for you
		ix.Rank(req)
	}
}
