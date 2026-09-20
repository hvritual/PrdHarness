package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hvritual/PrdHarness/internal/csd"
)

func TestCLIEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(path(name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	call := func(args ...string) []byte {
		t.Helper()
		var out, errs bytes.Buffer
		if status := run(args, &out, &errs); status != 0 {
			t.Fatalf("%v: exit %d: %s", args, status, errs.String())
		}
		return out.Bytes()
	}
	write("node.json", `{"id":"REQ-001","type":"requirement","title":"套餐升级","fields":{"behavior":"只收新增模块费用"}}`)
	write("goal.json", `{"id":"GOAL-001","type":"goal","title":"自助升级","fields":{}}`)
	write("patch.json", `{"set_fields":{"behavior":"支付成功后只开通已付费新增模块"}}`)
	call("create", "--id", "PRD-001", "--type", "prd", "--title", "套餐升级", "--out", path("v1.json"))
	call("add-node", "--file", path("v1.json"), "--node", path("node.json"), "--out", path("v2.json"))
	call("add-node", "--file", path("v2.json"), "--node", path("goal.json"), "--out", path("v3.json"))
	call("link-node", "--file", path("v3.json"), "--from", "REQ-001", "--type", "serves", "--to", "GOAL-001", "--out", path("v4.json"))
	call("update-node", "--file", path("v4.json"), "--id", "REQ-001", "--patch", path("patch.json"), "--out", path("v5.json"))
	if !bytes.Contains(call("validate", "--file", path("v5.json")), []byte(`"VALID"`)) {
		t.Fatal("validation status missing")
	}
	loaded := call("load", "--file", path("v5.json"))
	serialized := call("serialize", "--file", path("v5.json"))
	if !bytes.Equal(loaded, serialized) {
		t.Fatal("load/serialize differ")
	}
	if !bytes.Contains(call("semantic-hash", "--file", path("v5.json")), []byte("csd-semantic/v1:sha256:")) {
		t.Fatal("missing hash protocol")
	}
	var diff csd.Diff
	if err := json.Unmarshal(call("semantic-diff", "--before", path("v4.json"), "--after", path("v5.json")), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Path != "/nodes/REQ-001/fields/behavior" {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	// An existing output, including the source file, must never be overwritten.
	original, err := os.ReadFile(path("v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if run([]string{"serialize", "--file", path("v5.json"), "--out", path("v5.json")}, &out, &errs) != 1 {
		t.Fatal("overwrote source")
	}
	after, err := os.ReadFile(path("v5.json"))
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("input was damaged")
	}
}

func TestCLIRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(file, []byte(`{"id":"x","id":"y"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		exit int
	}{
		{[]string{}, 2}, {[]string{"unknown"}, 2}, {[]string{"validate", "--bad"}, 2}, {[]string{"validate", "extra"}, 2}, {[]string{"validate"}, 1}, {[]string{"validate", "--file", file}, 1}, {[]string{"semantic-diff", "--before", file, "--after", file}, 1}, {[]string{"create", "--id", "bad/id"}, 1},
	} {
		var out, errs bytes.Buffer
		if got := run(tt.args, &out, &errs); got != tt.exit {
			t.Fatalf("%v got %d want %d: %s", tt.args, got, tt.exit, errs.String())
		}
		if tt.exit == 1 && !json.Valid(bytes.TrimSpace(errs.Bytes())) {
			t.Fatal("error not JSON")
		}
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"create", "--help"}} {
		var out, errs bytes.Buffer
		if run(args, &out, &errs) != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal("help failed")
		}
	}
}

type brokenWriter struct{ short bool }

func (w brokenWriter) Write(p []byte) (int, error) {
	if w.short {
		return 0, nil
	}
	return 0, errors.New("broken pipe")
}
func TestOutputErrors(t *testing.T) {
	for _, w := range []io.Writer{brokenWriter{}, brokenWriter{short: true}} {
		var errs bytes.Buffer
		if run([]string{"create", "--id", "PRD-001", "--type", "prd", "--title", "Plan"}, w, &errs) != 1 {
			t.Fatal("ignored write error")
		}
	}
	dir := t.TempDir()
	large := filepath.Join(dir, "large")
	if err := os.WriteFile(large, bytes.Repeat([]byte(" "), csd.MaxJSONBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLimited(large); err == nil {
		t.Fatal("unbounded read")
	}
}
