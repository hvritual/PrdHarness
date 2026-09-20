package csd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hvritual/PrdHarness/internal/csd"
)

func must(t *testing.T, d *csd.Document, err error) *csd.Document {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func fresh(t *testing.T) *csd.Document {
	t.Helper()
	d, err := csd.NewDocument("PRD-001", "prd", "Plan upgrade")
	return must(t, d, err)
}
func val(t *testing.T, raw string) csd.Value {
	t.Helper()
	v, err := csd.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func node(t *testing.T, id string) csd.Node {
	return csd.Node{ID: id, Type: "requirement", Title: id, Fields: map[string]csd.Value{"behavior": val(t, `"charge new modules"`)}}
}
func add(t *testing.T, d *csd.Document, n csd.Node) *csd.Document {
	t.Helper()
	next, err := d.AddNode(n)
	return must(t, next, err)
}
func hash(t *testing.T, d *csd.Document) string {
	t.Helper()
	h, err := d.SemanticHash()
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func serialize(t *testing.T, d *csd.Document) []byte {
	t.Helper()
	b, err := d.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *csd.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v; want %s", err, want)
	}
}

func TestDocumentLifecycle(t *testing.T) {
	d := fresh(t)
	d = add(t, d, node(t, "REQ-001"))
	d = add(t, d, csd.Node{ID: "GOAL-001", Type: "goal", Title: "Self service"})
	linked, err := d.Link(csd.Edge{From: "REQ-001", Type: "serves", To: "GOAL-001"})
	d = must(t, linked, err)
	updated, err := d.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"behavior": val(t, `"charge only new modules"`)}})
	d = must(t, updated, err)
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Snapshot().Revision != 5 {
		t.Fatal("revision not advanced")
	}
	b := serialize(t, d)
	loaded, err := csd.Load(b)
	loaded = must(t, loaded, err)
	if !bytes.Equal(b, serialize(t, loaded)) || hash(t, d) != hash(t, loaded) {
		t.Fatal("round-trip changed document")
	}
}

func TestImmutableSnapshotsAndFailureAtomicity(t *testing.T) {
	base := fresh(t)
	n := node(t, "REQ-001")
	next := add(t, base, n)
	if len(base.Snapshot().Nodes) != 0 {
		t.Fatal("add mutated parent")
	}
	before := serialize(t, next)
	n.Fields["behavior"] = val(t, `"changed outside"`)
	s := next.Snapshot()
	s.Nodes[0].Fields["behavior"] = val(t, `false`)
	s.Nodes[0].Title = "outside"
	if !bytes.Equal(before, serialize(t, next)) {
		t.Fatal("mutable state escaped")
	}
	_, err := next.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"new_value": val(t, `true`)}, RemoveFields: []string{"missing"}})
	code(t, err, "NOT_FOUND")
	if !bytes.Equal(before, serialize(t, next)) {
		t.Fatal("failed update mutated state")
	}
	patch := csd.NodePatch{SetFields: map[string]csd.Value{"flag": val(t, `true`)}}
	updated, err := next.UpdateNode("REQ-001", patch)
	updated = must(t, updated, err)
	patch.SetFields["flag"] = val(t, `false`)
	b, _ := updated.Snapshot().Nodes[0].Fields["flag"].MarshalJSON()
	if string(b) != "true" {
		t.Fatal("patch map escaped")
	}
}

func TestRejectInvalidOperations(t *testing.T) {
	d := add(t, fresh(t), node(t, "REQ-001"))
	tests := []struct {
		name, want string
		fn         func() error
	}{
		{"duplicate id", "DUPLICATE_ID", func() error { _, e := d.AddNode(node(t, "REQ-001")); return e }},
		{"missing node", "NOT_FOUND", func() error { _, e := d.UpdateNode("REQ-404", csd.NodePatch{}); return e }},
		{"dangling edge", "DANGLING_EDGE", func() error { _, e := d.Link(csd.Edge{From: "REQ-001", Type: "serves", To: "GOAL-404"}); return e }},
		{"bad id", "INVALID_ID", func() error { _, e := csd.NewDocument("../x", "prd", "title"); return e }},
		{"bad type", "INVALID_TYPE", func() error { _, e := csd.NewDocument("PRD-001", "PRD", "title"); return e }},
		{"bad title", "INVALID_TITLE", func() error { _, e := csd.NewDocument("PRD-001", "prd", " "); return e }},
		{"empty value", "INVALID_VALUE", func() error {
			_, e := d.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"bad": {}}})
			return e
		}},
		{"set and remove", "INVALID_PATCH", func() error {
			_, e := d.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"behavior": val(t, `null`)}, RemoveFields: []string{"behavior"}})
			return e
		}},
		{"repeat remove", "INVALID_PATCH", func() error {
			_, e := d.UpdateNode("REQ-001", csd.NodePatch{RemoveFields: []string{"behavior", "behavior"}})
			return e
		}},
		{"forbidden field", "INVALID_FIELD", func() error {
			_, e := d.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"BAD": val(t, `true`)}})
			return e
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { code(t, tt.fn(), tt.want) })
	}
	linked, err := d.Link(csd.Edge{From: "REQ-001", Type: "related", To: "REQ-001"})
	linked = must(t, linked, err)
	_, err = linked.Link(csd.Edge{From: "REQ-001", Type: "related", To: "REQ-001"})
	code(t, err, "DUPLICATE_EDGE")
	// General graphs permit cycles. Domain schemas decide whether a relation must be acyclic.
	s := d.Snapshot()
	s.Revision = math.MaxInt64
	max, err := csd.FromSnapshot(s)
	max = must(t, max, err)
	_, err = max.AddNode(node(t, "REQ-002"))
	code(t, err, "REVISION_OVERFLOW")
	var nilDoc *csd.Document
	code(t, nilDoc.Validate(), "INVALID_DOCUMENT")
}

func TestStrictLoading(t *testing.T) {
	base := string(serialize(t, add(t, fresh(t), node(t, "REQ-001"))))
	tests := []struct{ name, data, want string }{
		{"duplicate", strings.Replace(base, `"id":"PRD-001"`, `"id":"PRD-001","id":"EVIL"`, 1), "DUPLICATE_KEY"},
		{"escaped duplicate", strings.Replace(base, `"id":"PRD-001"`, `"id":"PRD-001","\u0069d":"EVIL"`, 1), "DUPLICATE_KEY"},
		{"unknown", strings.Replace(base, `"schema":`, `"approved_by":"human","schema":`, 1), "UNKNOWN_FIELD"},
		{"case alias", strings.Replace(base, `"id":"PRD-001"`, `"ID":"PRD-001"`, 1), "UNKNOWN_FIELD"},
		{"missing", strings.Replace(base, `"revision":2,`, "", 1), "MISSING_FIELD"},
		{"version", strings.Replace(base, `csd/v1`, `csd/v2`, 1), "UNSUPPORTED_SCHEMA"},
		{"trailing", base + `{}`, "INVALID_JSON"},
		{"null nodes", strings.Replace(base, `"edges":[]`, `"edges":null`, 1), "INVALID_TYPE"},
		{"zero revision", strings.Replace(base, `"revision":2`, `"revision":0`, 1), "INVALID_REVISION"},
		{"nested unknown", strings.Replace(base, `"fields":`, `"policy":"bypass","fields":`, 1), "UNKNOWN_FIELD"},
		{"duplicate node", strings.Replace(base, `"nodes":[`, `"nodes":[{"id":"REQ-001","type":"requirement","title":"Dup","fields":{}},`, 1), "DUPLICATE_ID"},
		{"bad presentation", strings.Replace(base, `"collapsed":[]`, `"collapsed":["REQ-404"]`, 1), "INVALID_PRESENTATION"},
		{"dangling edge", strings.Replace(base, `"edges":[]`, `"edges":[{"from":"REQ-001","type":"serves","to":"MISSING"}]`, 1), "DANGLING_EDGE"},
		{"lone surrogate", strings.Replace(base, `Plan upgrade`, `\ud800`, 1), "INVALID_UNICODE"},
		{"low surrogate", strings.Replace(base, `Plan upgrade`, `\udc00`, 1), "INVALID_UNICODE"},
		{"invalid utf8", strings.Replace(base, `Plan upgrade`, string([]byte{0xff}), 1), "INVALID_JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := csd.Load([]byte(tt.data))
			code(t, err, tt.want)
			if d != nil {
				t.Fatal("invalid load returned a document")
			}
		})
	}
	valid := strings.Replace(base, `Plan upgrade`, `\ud83d\ude00`, 1)
	_, err := csd.Load([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	pretty := bytes.Buffer{}
	if err := json.Indent(&pretty, []byte(base), "", "  "); err != nil {
		t.Fatal(err)
	}
	d, err := csd.Load(pretty.Bytes())
	d = must(t, d, err)
	if string(serialize(t, d)) != base {
		t.Fatal("format affected serialization")
	}
}

func TestTypedValues(t *testing.T) {
	for _, raw := range []string{`null`, `true`, `false`, `"你好"`, `9223372036854775807`, `-9223372036854775808`, `[1,"x",null]`, `{"z":1,"a":2}`, `"\\ud800"`} {
		v := val(t, raw)
		b, err := v.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(b) {
			t.Fatal("bad value serialization")
		}
	}
	for _, raw := range []string{`1.5`, `1.0`, `1e0`, `9223372036854775808`, `-9223372036854775809`, `{"x":1,"x":2}`} {
		if _, err := csd.ParseValue([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if val(t, `{"b":-0,"a":1}`) != val(t, `{"a":1,"b":0}`) {
		t.Fatal("canonical value unstable")
	}
	if val(t, `[1,2]`) == val(t, `[2,1]`) {
		t.Fatal("ordered arrays lost meaning")
	}
	_, err := csd.ParseValue([]byte(strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70)))
	code(t, err, "LIMIT")
	_, err = csd.ParseValue(bytes.Repeat([]byte(" "), csd.MaxJSONBytes+1))
	code(t, err, "LIMIT")
}

func TestHashAndDiffInvariants(t *testing.T) {
	d := add(t, add(t, fresh(t), node(t, "REQ-002")), node(t, "REQ-001"))
	linked, err := d.Link(csd.Edge{From: "REQ-001", Type: "related", To: "REQ-002"})
	d = must(t, linked, err)
	linked, err = d.Link(csd.Edge{From: "REQ-002", Type: "related", To: "REQ-001"})
	d = must(t, linked, err)
	s := d.Snapshot()
	s.Nodes[0], s.Nodes[1] = s.Nodes[1], s.Nodes[0]
	s.Edges[0], s.Edges[1] = s.Edges[1], s.Edges[0]
	s.Revision += 10
	s.Presentation.NodeOrder = []string{"REQ-002", "REQ-001"}
	s.Presentation.Collapsed = []string{"REQ-001"}
	same, err := csd.FromSnapshot(s)
	same = must(t, same, err)
	if hash(t, d) != hash(t, same) {
		t.Fatal("storage/presentation/revision affected semantic hash")
	}
	diff, err := csd.SemanticDiff(d, same)
	if err != nil || len(diff.Changes) != 0 {
		t.Fatalf("unexpected diff %+v %v", diff, err)
	}
	if bytes.Equal(serialize(t, d), serialize(t, same)) {
		t.Fatal("presentation/revision not retained in full serialization")
	}
	s.Nodes[0].Fields["behavior"] = val(t, `"charge all modules"`)
	s.Title = "New title"
	changed, err := csd.FromSnapshot(s)
	changed = must(t, changed, err)
	diff, err = csd.SemanticDiff(d, changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 2 || diff.FromHash == diff.ToHash {
		t.Fatalf("missed meaningful changes: %+v", diff)
	}
	for i := 0; i < 20; i++ {
		again, err := csd.SemanticDiff(d, changed)
		if err != nil || !reflect.DeepEqual(diff, again) {
			t.Fatal("nondeterministic diff")
		}
	}
	other, err := csd.NewDocument("PRD-002", "prd", "Plan upgrade")
	other = must(t, other, err)
	_, err = csd.SemanticDiff(d, other)
	code(t, err, "IDENTITY_MISMATCH")
}

func TestDiffAddsRemovesAndNull(t *testing.T) {
	a := add(t, fresh(t), node(t, "REQ-001"))
	b, err := a.UpdateNode("REQ-001", csd.NodePatch{RemoveFields: []string{"behavior"}, SetFields: map[string]csd.Value{"nullable": val(t, `null`)}})
	b = must(t, b, err)
	b = add(t, b, node(t, "REQ-002"))
	next, err := b.Link(csd.Edge{From: "REQ-001", Type: "related", To: "REQ-002"})
	b = must(t, next, err)
	diff, err := csd.SemanticDiff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 4 {
		t.Fatalf("want four changes, got %+v", diff)
	}
	found := false
	for _, c := range diff.Changes {
		if c.Path == "/nodes/REQ-001/fields/nullable" {
			found = true
			if c.Kind != "add" || string(c.After) != "null" || len(c.Before) != 0 {
				t.Fatal("null confused with missing")
			}
		}
	}
	if !found {
		t.Fatal("missing null change")
	}
	reverse, err := csd.SemanticDiff(b, a)
	if err != nil || len(reverse.Changes) != 4 {
		t.Fatal("reverse removals lost")
	}
	for _, c := range reverse.Changes {
		if c.Path == "/nodes/REQ-002" && c.Kind != "remove" {
			t.Fatal("node removal missing")
		}
	}
}

func TestPatchCannotChangeIdentity(t *testing.T) {
	for _, raw := range []string{`{"id":"EVIL"}`, `{"type":"goal"}`, `{"Title":"alias"}`, `{"title":null}`, `{"set_fields":null}`} {
		if _, err := csd.DecodePatch([]byte(raw)); err == nil {
			t.Fatalf("accepted forbidden patch %s", raw)
		}
	}
	p, err := csd.DecodePatch([]byte(`{"title":"Renamed","set_fields":{"active":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	d := add(t, fresh(t), node(t, "REQ-001"))
	updated, err := d.UpdateNode("REQ-001", p)
	updated = must(t, updated, err)
	if updated.Snapshot().Nodes[0].ID != "REQ-001" || updated.Snapshot().Nodes[0].Title != "Renamed" {
		t.Fatal("identity/title mismatch")
	}
}

func TestGoldenSemanticBytes(t *testing.T) {
	d := fresh(t)
	want := `{"schema":"csd/v1","hash_version":"csd-semantic/v1","id":"PRD-001","type":"prd","title":"Plan upgrade","nodes":[],"edges":[]}`
	b, err := d.SemanticBytes()
	if err != nil || string(b) != want {
		t.Fatalf("canonical protocol drift: %s %v", b, err)
	}
	// Golden digest is computed independently with Python hashlib, not with this implementation.
	const wantHash = "csd-semantic/v1:sha256:f5dc754a61889869033d1691a336deba139b9c217ed741cf161bd5a1b9484c18"
	if got := hash(t, d); got != wantHash {
		t.Fatalf("digest drift: %s", got)
	}
}

func TestConcurrentReadAndDerivedWrites(t *testing.T) {
	d := add(t, fresh(t), node(t, "REQ-001"))
	original := hash(t, d)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := csd.ParseValue([]byte(fmt.Sprint(i)))
			if err != nil {
				t.Error(err)
				return
			}
			next, err := d.UpdateNode("REQ-001", csd.NodePatch{SetFields: map[string]csd.Value{"index": v}})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = next.SemanticHash(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if hash(t, d) != original {
		t.Fatal("shared snapshot changed")
	}
}

func FuzzLoadRoundTrip(f *testing.F) {
	f.Add([]byte(`{"schema":"csd/v1","id":"PRD-001","type":"prd","title":"Plan","revision":1,"nodes":[],"edges":[]}`))
	f.Add([]byte(`{"id":"a","id":"b"}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := csd.Load(b)
		if err != nil {
			return
		}
		canonical, err := d.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		again, err := csd.Load(canonical)
		if err != nil {
			t.Fatal(err)
		}
		next, err := again.Serialize()
		if err != nil || !bytes.Equal(canonical, next) {
			t.Fatal("unstable roundtrip")
		}
		diff, err := csd.SemanticDiff(d, again)
		if err != nil || len(diff.Changes) != 0 || diff.FromHash != diff.ToHash {
			t.Fatal("hash/diff mismatch")
		}
	})
}
