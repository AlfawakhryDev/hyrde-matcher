package match

import "slices"

// Request describes a milestone that needs an engineer.
type Request struct {
	MilestoneID    string   `json:"milestoneId"`
	Integrations   []string `json:"integrations"`   // systems the work touches
	PayoutCents    int64    `json:"payoutCents"`    // what Hyrde pays the engineer
	EstimatedHours float64  `json:"estimatedHours"` // from the milestone breakdown
	Limit          int      `json:"limit"`          // how many to return, default 5
}

// Candidate is one ranked engineer with the reasons for the score, so a
// delivery lead can see why someone was suggested.
type Candidate struct {
	EngineerID  string   `json:"engineerId"`
	Name        string   `json:"name"`
	Score       float64  `json:"score"`
	Coverage    float64  `json:"coverage"`    // share of required integrations they have
	Reliability float64  `json:"reliability"` // smoothed on-time and approval rate
	Headroom    float64  `json:"headroom"`    // free capacity
	RateFit     float64  `json:"rateFit"`     // 1 when their rate fits the payout
	Missing     []string `json:"missing,omitempty"`
}

// Weights sum to 1, so a score reads as a percentage. Coverage dominates: an
// engineer who has never touched Procore is the wrong person, however reliable.
const (
	wCoverage    = 0.50
	wReliability = 0.25
	wHeadroom    = 0.15
	wRateFit     = 0.10
	defaultLimit = 5
	maxLimit     = 50
)

// Rank returns the best engineers for a milestone, best first.
// Candidates come from the inverted index, so cost grows with the number of
// engineers who share a skill, not with the size of the bench.
func (ix *Index) Rank(req Request) []Candidate {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)

	want := make([]string, 0, len(req.Integrations))
	for _, s := range req.Integrations {
		if n := normalize(s); n != "" && !slices.Contains(want, n) {
			want = append(want, n)
		}
	}

	affordable := 0.0 // the hourly rate the payout supports; 0 = unknown
	if req.PayoutCents > 0 && req.EstimatedHours > 0 {
		affordable = float64(req.PayoutCents) / 100 / req.EstimatedHours
	}

	// Score every candidate cheaply (position + number, no allocation per
	// engineer) and keep only the best `limit` in a small sorted slice. The
	// full Candidate, with its "missing" list, is built for the winners only.
	top := make([]scored, 0, limit+1)
	consider := func(p int32) {
		e := &ix.engineers[p]
		s := scored{pos: p, coverage: 1, rateFit: 1}
		if len(want) > 0 {
			have := 0
			for _, sk := range want {
				if _, ok := e.skills[sk]; ok {
					have++
				}
			}
			s.coverage = float64(have) / float64(len(want))
		}
		if affordable > 0 && e.HourlyRateUSD > 0 && float64(e.HourlyRateUSD) > affordable {
			s.rateFit = affordable / float64(e.HourlyRateUSD)
		}
		s.score = wCoverage*s.coverage + wReliability*e.Reliability() + wHeadroom*e.Headroom() + wRateFit*s.rateFit
		if len(top) == limit && !ix.better(s, top[len(top)-1]) {
			return
		}
		i, _ := slices.BinarySearchFunc(top, s, func(a, b scored) int {
			if ix.better(a, b) {
				return -1
			}
			return 1
		})
		top = slices.Insert(top, i, s)
		if len(top) > limit {
			top = top[:limit]
		}
	}

	if len(want) == 0 {
		// No integrations named: everyone is a candidate, ranked on the rest.
		for p := range ix.engineers {
			consider(int32(p))
		}
	} else {
		// Engineers who share several skills appear in several posting lists;
		// a bitset (one bit per engineer) makes sure each is scored once.
		seen := make([]uint64, len(ix.engineers)/64+1)
		for _, sk := range want {
			for _, p := range ix.bySkill[sk] {
				if seen[p/64]&(1<<(p%64)) != 0 {
					continue
				}
				seen[p/64] |= 1 << (p % 64)
				consider(p)
			}
		}
	}

	out := make([]Candidate, len(top))
	for i, s := range top {
		e := &ix.engineers[s.pos]
		c := Candidate{EngineerID: e.ID, Name: e.Name, Score: s.score, Coverage: s.coverage,
			Reliability: e.Reliability(), Headroom: e.Headroom(), RateFit: s.rateFit}
		for _, sk := range want {
			if _, ok := e.skills[sk]; !ok {
				c.Missing = append(c.Missing, sk)
			}
		}
		out[i] = c
	}
	return out
}

type scored struct {
	pos      int32
	score    float64
	coverage float64
	rateFit  float64
}

// better orders by score, highest first; ties break on engineer id so the
// same request always returns the same list.
func (ix *Index) better(a, b scored) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	return ix.engineers[a.pos].ID < ix.engineers[b.pos].ID
}
