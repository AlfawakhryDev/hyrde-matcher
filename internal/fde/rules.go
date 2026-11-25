package fde

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Money is always integer cents. Never float: 0.1 + 0.2 != 0.3.

// PricingSettings are the defaults; a client's own prices override them.
type PricingSettings struct {
	DiscoverySprintCents   int64
	ContinuousMonthlyCents int64
	MarginBps              int64 // basis points: 2000 = 20%
}

// CompanyPricing uses pointers (*int64) so "not set" (nil) is different from
// a real 0. A design partner's $0 sprint must not fall back to $3,000.
type CompanyPricing struct {
	DiscoverySprintCents   *int64
	ContinuousMonthlyCents *int64
	MarginBps              *int64
}

func or(v *int64, fallback int64) int64 {
	if v != nil {
		return *v // *v reads the value the pointer points at
	}
	return fallback
}

// EffectivePricing returns the prices this client actually pays.
func EffectivePricing(s PricingSettings, c CompanyPricing) PricingSettings {
	return PricingSettings{
		DiscoverySprintCents:   or(c.DiscoverySprintCents, s.DiscoverySprintCents),
		ContinuousMonthlyCents: or(c.ContinuousMonthlyCents, s.ContinuousMonthlyCents),
		MarginBps:              or(c.MarginBps, s.MarginBps),
	}
}

// ClientPriceCents is the one price the client sees: payout plus margin,
// rounded to whole dollars.
func ClientPriceCents(payoutCents, marginBps int64) int64 {
	withMargin := float64(payoutCents*(10_000+marginBps)) / 10_000
	return int64(math.Round(withMargin/100)) * 100
}

type Engineer struct {
	UserID string
	Skills []string // a slice: a growable view over an array
	Status string
}

type RankedEngineer struct {
	Engineer // embedding: RankedEngineer gets all of Engineer's fields
	Overlap  int
}

// RankEngineers keeps approved engineers only, most overlapping skills first.
func RankEngineers(engineers []Engineer, integrations []string) []RankedEngineer {
	want := make(map[string]bool, len(integrations)) // Go's set is a map[T]bool
	for _, s := range integrations {
		want[strings.ToLower(s)] = true
	}
	var out []RankedEngineer
	for _, e := range engineers {
		if e.Status != "approved" {
			continue
		}
		n := 0
		for _, s := range e.Skills {
			if want[strings.ToLower(s)] { // a missing key reads as false
				n++
			}
		}
		out = append(out, RankedEngineer{Engineer: e, Overlap: n})
	}
	// SliceStable keeps equal-overlap engineers in their original order,
	// matching JavaScript's stable Array.sort.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Overlap > out[j].Overlap })
	return out
}

type Ticket struct {
	Title              string
	Problem            string
	AcceptanceCriteria string
	Integrations       []string
	Status             string
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// TicketsToMarkdown renders tickets for pasting into any tracker.
func TicketsToMarkdown(company string, tickets []Ticket) string {
	var b strings.Builder // the efficient way to build a string piece by piece
	fmt.Fprintf(&b, "# %s: tickets\n\n", company)
	for i, t := range tickets {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %s\nStatus: %s", t.Title, t.Status)
		if len(t.Integrations) > 0 {
			fmt.Fprintf(&b, " · Integrations: %s", strings.Join(t.Integrations, ", "))
		}
		fmt.Fprintf(&b, "\n\n### Problem\n%s\n\n### Acceptance criteria\n%s", dash(t.Problem), dash(t.AcceptanceCriteria))
	}
	b.WriteString("\n")
	return b.String()
}

// TicketsToCSV uses the standard library's CSV writer, which handles quoting
// of commas, quotes and newlines; the TypeScript version had to do it by hand.
func TicketsToCSV(tickets []Ticket) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Title", "Description", "Labels", "Status"})
	for _, t := range tickets {
		desc := strings.TrimSpace(t.Problem + "\n\nAcceptance criteria:\n" + t.AcceptanceCriteria)
		_ = w.Write([]string{t.Title, desc, strings.Join(t.Integrations, ","), t.Status})
	}
	w.Flush()
	// Errors are values in Go: functions return them, callers check them.
	return buf.String(), w.Error()
}
