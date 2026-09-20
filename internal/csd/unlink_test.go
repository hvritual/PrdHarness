package csd

import "testing"

func TestUnlinkReturnsNewSnapshot(t *testing.T) {
	doc, err := NewDocument("PRD-001", "prd", "Plan")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = doc.AddNode(Node{ID: "REQ-001", Type: "requirement", Title: "Req", Fields: map[string]Value{}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = doc.AddNode(Node{ID: "GOAL-001", Type: "goal", Title: "Goal", Fields: map[string]Value{}})
	if err != nil {
		t.Fatal(err)
	}
	edge := Edge{From: "REQ-001", Type: "serves", To: "GOAL-001"}
	linked, err := doc.Link(edge)
	if err != nil {
		t.Fatal(err)
	}
	unlinked, err := linked.Unlink(edge)
	if err != nil {
		t.Fatal(err)
	}
	if len(linked.Snapshot().Edges) != 1 || len(unlinked.Snapshot().Edges) != 0 {
		t.Fatal("unlink mutated parent or retained edge")
	}
	if unlinked.Snapshot().Revision != linked.Snapshot().Revision+1 {
		t.Fatal("unlink did not advance revision")
	}
	if _, err = unlinked.Unlink(edge); err == nil {
		t.Fatal("missing edge was silently ignored")
	}
}
