package fde

import (
	"math"
	"testing"
)

// Go has no test framework to install: files ending in _test.go with
// functions named TestXxx(t *testing.T) run with `go test ./...`.

func TestPriceMilestoneRange(t *testing.T) {
	got := PriceMilestone("Development", 10, "mid")
	want := MilestonePrice{Hours: 10, RateUSD: 45, Seniority: Mid, MidUSD: 450, LowUSD: 350, HighUSD: 550,
		Basis: "About 10 hours of development work at $45/hr for a mid-level specialist."}
	if got != want { // structs of comparable fields compare with ==
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// A table-driven test: the idiomatic Go way to check many cases. Each case
// is an anonymous struct in a slice; t.Run gives each its own name in output.
func TestPriceMilestoneEdges(t *testing.T) {
	cases := []struct {
		name          string
		category      string
		hours         float64
		seniority     string
		wantHours     int
		wantRate      int
		wantSeniority Seniority
	}{
		{"junior multiplier", "Development", 10, "junior", 10, 27, Junior},
		{"senior multiplier rounds", "Development", 10, "senior", 10, 68, Senior},
		{"unknown seniority is mid", "Development", 10, "wizard", 10, 45, Mid},
		{"zero hours becomes 1", "Design", 0, "mid", 1, 40, Mid},
		{"NaN hours becomes 1", "Design", math.NaN(), "mid", 1, 40, Mid},
		{"negative hours becomes 1", "Design", -5, "mid", 1, 40, Mid},
		{"runaway hours capped", "Design", 10_000, "mid", 400, 40, Mid},
		{"unknown category falls back", "Basket weaving", 10, "mid", 10, 38, Mid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := PriceMilestone(c.category, c.hours, c.seniority)
			if p.Hours != c.wantHours || p.RateUSD != c.wantRate || p.Seniority != c.wantSeniority {
				t.Errorf("got hours=%d rate=%d seniority=%s", p.Hours, p.RateUSD, p.Seniority)
			}
		})
	}
}

func TestPriceMilestoneInvariants(t *testing.T) {
	for category := range CategoryRateUSD { // ranging over a map yields keys (in random order)
		for _, s := range []string{"junior", "mid", "senior"} {
			for _, h := range []float64{1, 7, 40, 400} {
				p := PriceMilestone(category, h, s)
				if p.LowUSD < 50 || p.LowUSD > p.MidUSD || p.MidUSD > p.HighUSD {
					t.Errorf("%s/%s/%v: bad range %d ≤ %d ≤ %d", category, s, h, p.LowUSD, p.MidUSD, p.HighUSD)
				}
			}
		}
	}
}
