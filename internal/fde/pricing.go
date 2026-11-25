// Package fde holds Hyrde's business rules: what a milestone costs, what the
// client pays, who gets matched, and how tickets are exported. Pure functions
// only, so every number is testable without a database or a network.
//
// It is a line-for-line port of web/lib/pricing.ts and web/lib/fde.ts; the
// tests pin the same answers so both sides agree while they coexist.
package fde

import (
	"fmt"
	"math"
	"strings"
)

// Seniority is a named string type: still a string underneath, but the
// compiler won't let you pass any old string where a Seniority is expected.
type Seniority string

const (
	Junior Seniority = "junior"
	Mid    Seniority = "mid"
	Senior Seniority = "senior"
)

// CategoryRateUSD is the blended hourly rate per task category.
// A map literal: map[KeyType]ValueType{...}.
var CategoryRateUSD = map[string]int{
	"Development":       45,
	"Data":              50,
	"Design":            40,
	"Marketing":         32,
	"Social media":      30,
	"Copywriting":       30,
	"Technical writing": 40,
	"Other":             38,
}

var seniorityMultiplier = map[Seniority]float64{Junior: 0.6, Mid: 1.0, Senior: 1.5}

const (
	fallbackRate = 38
	spread       = 0.2 // quotes are a ±20% range, not false precision
	minHours     = 1
	maxHours     = 400 // ~10 weeks of one person; beyond this the milestone is wrong
)

// MilestonePrice is a struct: Go's only way to group fields. Capitalised
// fields are exported (visible outside the package); the `json:"..."` tags
// name them when the struct is encoded, so the API speaks camelCase like the
// TypeScript side.
type MilestonePrice struct {
	Hours     int       `json:"hours"`
	RateUSD   int       `json:"rateUsd"`
	Seniority Seniority `json:"seniority"`
	MidUSD    int       `json:"midUsd"`
	LowUSD    int       `json:"lowUsd"`
	HighUSD   int       `json:"highUsd"`
	Basis     string    `json:"basis"` // one sentence a client can read: why this number
}

// RateFor returns the hourly rate for a category. The two-value map lookup
// ("comma ok") tells a missing key apart from a zero value.
func RateFor(category string) int {
	if rate, ok := CategoryRateUSD[category]; ok {
		return rate
	}
	return fallbackRate
}

func round50(n float64) int {
	return max(50, int(math.Round(n/50))*50)
}

// PriceMilestone turns estimated effort into a quote. The model estimates
// hours, never money: same input, same price, every time.
func PriceMilestone(category string, rawHours float64, rawSeniority string) MilestonePrice {
	hours := minHours
	if !math.IsNaN(rawHours) {
		hours = min(maxHours, max(minHours, int(math.Round(rawHours))))
	}

	seniority := Seniority(rawSeniority)
	if seniority != Junior && seniority != Senior {
		seniority = Mid
	}

	rate := int(math.Round(float64(RateFor(category)) * seniorityMultiplier[seniority]))
	mid := float64(hours * rate)

	return MilestonePrice{
		Hours:     hours,
		RateUSD:   rate,
		Seniority: seniority,
		MidUSD:    round50(mid),
		LowUSD:    round50(mid * (1 - spread)),
		HighUSD:   round50(mid * (1 + spread)),
		Basis: fmt.Sprintf("About %d hours of %s work at $%d/hr for a %s-level specialist.",
			hours, strings.ToLower(category), rate, seniority),
	}
}
