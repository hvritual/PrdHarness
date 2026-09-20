package operations

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hvritual/PrdHarness/internal/csd"
)

func value(t *testing.T, raw string) csd.Value {
	t.Helper()
	v, err := csd.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func actor() ActorContext { return ActorContext{Class: "human", Subject: "local:test"} }
func errCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v want %s", err, want)
	}
}
func strptr(s string) *string { return &s }
func fullCreate(t *testing.T, id string) Command {
	t.Helper()
	return Command{Version: CommandVersion, ID: id, DocumentID: "PRD-001", ExpectedRevision: 0, Actor: actor(), Operations: []Operation{
		{Kind: "create_document", Document: &DocumentInput{Type: "prd", Title: "套餐升级"}},
		{Kind: "add_node", Node: &csd.Node{ID: "REQ-001", Type: "requirement", Title: "升级", Fields: map[string]csd.Value{"behavior": value(t, `"charge delta"`)}}},
		{Kind: "add_node", Node: &csd.Node{ID: "GOAL-001", Type: "goal", Title: "自助", Fields: map[string]csd.Value{}}},
		{Kind: "link", Edge: &csd.Edge{From: "REQ-001", Type: "serves", To: "GOAL-001"}},
		{Kind: "update_node", NodeID: "REQ-001", Patch: &csd.NodePatch{SetFields: map[string]csd.Value{"behavior": value(t, `"charge only delta"`)}}},
		{Kind: "unlink", Edge: &csd.Edge{From: "REQ-001", Type: "serves", To: "GOAL-001"}},
		{Kind: "deprecate_node", NodeID: "REQ-001"},
	}}
}

func TestEngineBatchReceiptAndReplay(t *testing.T) {
	e := NewEngine()
	e.now = func() time.Time { return time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC) }
	cmd := fullCreate(t, "CMD-001")
	tr, err := e.Apply(State{}, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Commit || tr.Result.Replayed || tr.Result.Document.Revision != 7 || tr.Result.Receipt.BaseRevision != 0 || tr.Result.Receipt.ResultRevision != 7 {
		t.Fatalf("bad transition %+v", tr)
	}
	if tr.Result.Receipt.Actor.IdentityAssurance != "caller_asserted_local" {
		t.Fatal("local trust boundary missing")
	}
	replay, err := e.Apply(tr.State, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Commit || !replay.Result.Replayed || len(replay.State.Receipts) != 1 || replay.State.Document.Snapshot().Revision != 7 {
		t.Fatal("replay changed state")
	}
	changed := cmd
	changed.Actor = ActorContext{Class: "ai", Subject: "agent:test"}
	_, err = e.Apply(tr.State, changed)
	errCode(t, err, "IDEMPOTENCY_CONFLICT")
}
func TestEngineRevisionConflictAndBatchFailureArePure(t *testing.T) {
	e := NewEngine()
	base, err := e.Apply(State{}, fullCreate(t, "CMD-001"))
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, _ := base.State.Document.SemanticHash()
	stale := Command{Version: CommandVersion, ID: "CMD-002", DocumentID: "PRD-001", ExpectedRevision: 6, Actor: actor(), Operations: []Operation{{Kind: "deprecate_node", NodeID: "GOAL-001"}}}
	_, err = e.Apply(base.State, stale)
	errCode(t, err, "REVISION_CONFLICT")
	title := "changed"
	batch := Command{Version: CommandVersion, ID: "CMD-003", DocumentID: "PRD-001", ExpectedRevision: 7, Actor: actor(), Operations: []Operation{{Kind: "update_node", NodeID: "GOAL-001", Patch: &csd.NodePatch{Title: &title}}, {Kind: "unlink", Edge: &csd.Edge{From: "REQ-001", Type: "missing", To: "GOAL-001"}}}}
	_, err = e.Apply(base.State, batch)
	if err == nil {
		t.Fatal("bad batch accepted")
	}
	afterHash, _ := base.State.Document.SemanticHash()
	if beforeHash != afterHash || base.State.Document.Snapshot().Revision != 7 || len(base.State.Receipts) != 1 {
		t.Fatal("engine mutated input state")
	}
}
func TestRejectNoSemanticChangeAndDeprecationState(t *testing.T) {
	e := NewEngine()
	base, err := e.Apply(State{}, fullCreate(t, "CMD-001"))
	if err != nil {
		t.Fatal(err)
	}
	same := Command{Version: CommandVersion, ID: "CMD-002", DocumentID: "PRD-001", ExpectedRevision: 7, Actor: actor(), Operations: []Operation{{Kind: "update_node", NodeID: "GOAL-001", Patch: &csd.NodePatch{Title: strptr("自助")}}}}
	_, err = e.Apply(base.State, same)
	errCode(t, err, "NO_SEMANTIC_CHANGE")
	dep := Command{Version: CommandVersion, ID: "CMD-003", DocumentID: "PRD-001", ExpectedRevision: 7, Actor: actor(), Operations: []Operation{{Kind: "deprecate_node", NodeID: "REQ-001"}}}
	_, err = e.Apply(base.State, dep)
	errCode(t, err, "ALREADY_DEPRECATED")
}
func TestDecodeCommandStrictnessAndHash(t *testing.T) {
	valid := `{"version":"semantic-command/v1","id":"CMD-001","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"local:test"},"operations":[{"kind":"create_document","document":{"type":"prd","title":"Plan"}}]}`
	cmd, err := DecodeCommand([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	h1, err := CommandHash(cmd)
	if err != nil || h1 == "" {
		t.Fatal("missing hash")
	}
	for _, raw := range []string{`{"version":"semantic-command/v1","id":"CMD-001","id":"CMD-002","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"x"},"operations":[]}`, `{"version":"semantic-command/v1","id":"CMD-001","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"x"},"operations":[{"kind":"create_document","document":{"type":"prd","title":"Plan"},"evil":true}]}`, `{"version":"semantic-command/v1","id":"CMD-001","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"\ud800"},"operations":[{"kind":"create_document","document":{"type":"prd","title":"Plan"}}]}`} {
		if _, err := DecodeCommand([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func FuzzDecodeCommand(f *testing.F) {
	f.Add([]byte(`{"version":"semantic-command/v1","id":"CMD-001","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"local:test"},"operations":[{"kind":"create_document","document":{"type":"prd","title":"Plan"}}]}`))
	f.Add([]byte(`{"id":"a","id":"b"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		cmd, err := DecodeCommand(data)
		if err != nil {
			return
		}
		h1, err := CommandHash(cmd)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(cmd)
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeCommand(b)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := CommandHash(again)
		if err != nil {
			t.Fatal(err)
		}
		if h1 != h2 {
			t.Fatal("command canonicalization changed hash")
		}
	})
}
