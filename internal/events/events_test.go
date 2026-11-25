package events

import "testing"

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

func TestDelta(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want Delta
		ok   bool
	}{
		{"assigned books a slot", Event{Type: MilestoneAssigned}, Delta{Active: 1}, true},
		{"on-time delivery", Event{Type: MilestoneDelivered, OnTime: yes()}, Delta{Active: -1, Delivered: 1, OnTime: 1}, true},
		{"late delivery", Event{Type: MilestoneDelivered, OnTime: no()}, Delta{Active: -1, Delivered: 1}, true},
		{"approved review", Event{Type: MilestoneReviewed, Approved: yes()}, Delta{Reviewed: 1, Approved: 1}, true},
		{"changes requested", Event{Type: MilestoneReviewed, Approved: no()}, Delta{Reviewed: 1}, true},
		{"match audit touches nobody", Event{Type: MatchServed}, Delta{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.ev.Delta()
			if got != c.want || ok != c.ok {
				t.Fatalf("Delta() = %+v, %v; want %+v, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	bad := []Event{
		{},                          // no id
		{ID: "1", MilestoneID: "m"}, // no type
		{ID: "1", MilestoneID: "m", Type: MilestoneDelivered, EngineerID: "e"}, // no onTime
		{ID: "1", MilestoneID: "m", Type: MilestoneAssigned},                   // no engineer
	}
	for i, e := range bad {
		if e.Validate() == nil {
			t.Errorf("case %d: want an error", i)
		}
	}
	ok := Event{ID: "1", MilestoneID: "m", Type: MilestoneReviewed, EngineerID: "e", Approved: yes()}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
}
