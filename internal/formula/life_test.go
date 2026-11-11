package formula

import (
	"reflect"
	"testing"
)

// TestLife_TellKeepsTheOther: a life copied from another and told
// further leaves the other's states as they were (ADR-0047, item 6). A
// commit clones its row for every step, and the row before must go on
// answering for the moment before.
func TestLife_TellKeepsTheOther(t *testing.T) {
	var a Life
	a.Tell(LifeEvent{Kind: LifeCreated, To: "draft", At: 100, By: "ann"})
	a.Tell(LifeEvent{Kind: LifeMoved, From: "draft", To: "review", At: 200, By: "bob"})
	// Room to grow in place, as a list that TellLife built has.
	a.States = append(make([]StateLife, 0, 8), a.States...)
	before := append([]StateLife(nil), a.States...)

	b, c := a, a
	b.Tell(LifeEvent{Kind: LifeMoved, From: "review", To: "closed", At: 300, By: "cy"})
	c.Tell(LifeEvent{Kind: LifeMoved, From: "review", To: "draft", At: 400, By: "dee"})
	if !reflect.DeepEqual(a.States, before) {
		t.Errorf("the life told from changed:\n got %+v\nwant %+v", a.States, before)
	}
	if st := b.state("closed"); st == nil || st.FirstEnteredBy != "cy" || b.state("review").LastLeft != 300 || b.state("draft").Entries != 1 {
		t.Errorf("the first copy: %+v", b.States)
	}
	if st := c.state("draft"); st == nil || st.Entries != 2 || st.LastEnteredBy != "dee" || c.state("review").LastLeft != 400 || c.state("closed") != nil {
		t.Errorf("the second copy: %+v", c.States)
	}
}
