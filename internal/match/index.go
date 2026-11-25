package match

import "time"

// Index is an immutable snapshot of matchable engineers. Readers never lock:
// a new Index is built off to the side and published with one atomic store.
type Index struct {
	engineers []indexed
	bySkill   map[string][]int32 // inverted index: skill -> positions in engineers
	BuiltAt   time.Time
}

type indexed struct {
	Engineer
	skills map[string]struct{} // struct{} takes no memory: a set
}

// NewIndex keeps only engineers who can take work now.
func NewIndex(all []Engineer, builtAt time.Time) *Index {
	ix := &Index{bySkill: make(map[string][]int32), BuiltAt: builtAt}
	for _, e := range all {
		if e.Status != "approved" || e.Headroom() == 0 {
			continue
		}
		set := make(map[string]struct{}, len(e.Skills))
		for _, s := range e.Skills {
			set[normalize(s)] = struct{}{}
		}
		pos := int32(len(ix.engineers))
		ix.engineers = append(ix.engineers, indexed{Engineer: e, skills: set})
		for s := range set {
			ix.bySkill[s] = append(ix.bySkill[s], pos)
		}
	}
	return ix
}

// Len is the number of engineers available for matching.
func (ix *Index) Len() int { return len(ix.engineers) }
