package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestControlledStoreCLI(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	command := filepath.Join(dir, "command.json")
	raw := `{"version":"semantic-command/v1","id":"CMD-001","document_id":"PRD-001","expected_revision":0,"actor":{"class":"human","subject":"local:test"},"operations":[{"kind":"create_document","document":{"type":"prd","title":"套餐升级"}},{"kind":"add_node","node":{"id":"REQ-001","type":"requirement","title":"升级","fields":{"behavior":"delta"}}}]}`
	if err := os.WriteFile(command, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"apply", "--store", store, "--command", command}, &out, &errs); code != 0 {
		t.Fatalf("apply=%d %s", code, errs.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["replayed"] != false {
		t.Fatal("unexpected replay")
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"get", "--store", store, "--id", "PRD-001"}, &out, &errs); code != 0 {
		t.Fatalf("get=%d %s", code, errs.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"revision":2`)) {
		t.Fatalf("wrong current %s", out.String())
	}
	out.Reset()
	errs.Reset()
	if code := run([]string{"apply", "--store", store, "--command", command}, &out, &errs); code != 0 {
		t.Fatalf("replay=%d %s", code, errs.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"replayed":true`)) {
		t.Fatal("replay not reported")
	}
}

func TestStoreCLIFailureIsStructured(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"version":"semantic-command/v1","id":"CMD-001","id":"CMD-X"}`), 0600)
	var out, errs bytes.Buffer
	if code := run([]string{"apply", "--store", filepath.Join(dir, "store"), "--command", bad}, &out, &errs); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !json.Valid(bytes.TrimSpace(errs.Bytes())) {
		t.Fatalf("not json: %s", errs.String())
	}
}
