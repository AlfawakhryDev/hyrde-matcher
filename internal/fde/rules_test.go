package fde

import (
	"strings"
	"testing"
)

func ptr(v int64) *int64 { return &v } // &v takes the address of v

func TestClientPriceCents(t *testing.T) {
	cases := []struct{ payout, bps, want int64 }{
		{100_000, 2000, 120_000},
		{123_456, 2000, 148_100},
		{50_000, 0, 50_000},
	}
	for _, c := range cases {
		if got := ClientPriceCents(c.payout, c.bps); got != c.want {
			t.Errorf("ClientPriceCents(%d, %d) = %d, want %d", c.payout, c.bps, got, c.want)
		}
	}
}

func TestDesignPartnerZeroIsNotDefault(t *testing.T) {
	s := PricingSettings{300_000, 200_000, 2000}
	got := EffectivePricing(s, CompanyPricing{DiscoverySprintCents: ptr(0)})
	if got != (PricingSettings{0, 200_000, 2000}) {
		t.Fatalf("got %+v", got)
	}
}

func TestRankEngineers(t *testing.T) {
	got := RankEngineers([]Engineer{
		{"a", []string{"Procore"}, "approved"},
		{"b", []string{"procore", "QuickBooks"}, "approved"},
		{"c", []string{"Procore", "QuickBooks"}, "trial"},
	}, []string{"Procore", "QuickBooks"})
	if len(got) != 2 || got[0].UserID != "b" || got[0].Overlap != 2 || got[1].UserID != "a" || got[1].Overlap != 1 {
		t.Fatalf("got %+v", got)
	}
}

var sample = []Ticket{{`Sync "draws"`, "Manual, slow", "- syncs nightly", []string{"Procore", "QuickBooks"}, "approved"}}

func TestTicketsToCSV(t *testing.T) {
	got, err := TicketsToCSV(sample)
	if err != nil {
		t.Fatal(err)
	}
	want := "Title,Description,Labels,Status\n\"Sync \"\"draws\"\"\",\"Manual, slow\n\nAcceptance criteria:\n- syncs nightly\",\"Procore,QuickBooks\",approved\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestTicketsToMarkdown(t *testing.T) {
	got := TicketsToMarkdown("Acme", sample)
	if !strings.Contains(got, "## Sync \"draws\"\nStatus: approved · Integrations: Procore, QuickBooks") {
		t.Fatalf("got %q", got)
	}
}
