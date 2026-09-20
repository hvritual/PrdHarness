package filestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hvritual/PrdHarness/internal/csd"
	"github.com/hvritual/PrdHarness/internal/operations"
)

func fvalue(t *testing.T, raw string) csd.Value {
	t.Helper()
	v, err := csd.ParseValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func fcreate(t *testing.T, id string) operations.Command {
	t.Helper()
	return operations.Command{Version: operations.CommandVersion, ID: id, DocumentID: "PRD-001", ExpectedRevision: 0, Actor: operations.ActorContext{Class: "human", Subject: "local:test"}, Operations: []operations.Operation{
		{Kind: "create_document", Document: &operations.DocumentInput{Type: "prd", Title: "Plan"}},
		{Kind: "add_node", Node: &csd.Node{ID: "REQ-001", Type: "requirement", Title: "Requirement", Fields: map[string]csd.Value{"behavior": fvalue(t, `"A"`)}}},
	}}
}
func fupdate(t *testing.T, id string, revision int64, title string) operations.Command {
	t.Helper()
	return operations.Command{Version: operations.CommandVersion, ID: id, DocumentID: "PRD-001", ExpectedRevision: revision, Actor: operations.ActorContext{Class: "human", Subject: "local:test"}, Operations: []operations.Operation{{Kind: "update_node", NodeID: "REQ-001", Patch: &csd.NodePatch{Title: &title}}}}
}
func anyCode(err error) string {
	var oe *operations.Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	var ce *csd.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestPersistReplayAndReopen(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	svc := store
	cmd := fcreate(t, "CMD-001")
	first, err := svc.Apply(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.Document.Revision != 2 || first.Replayed {
		t.Fatalf("bad first result %+v", first)
	}
	other, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := other.Apply(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.Document.Revision != 2 {
		t.Fatal("durable idempotent replay failed")
	}
	state, err := other.Read(context.Background(), "PRD-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Receipts) != 1 || state.Document.Snapshot().Revision != 2 {
		t.Fatal("bad persisted state")
	}
	changed := cmd
	changed.Actor.Subject = "other"
	_, err = other.Apply(context.Background(), changed)
	if anyCode(err) != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentCASAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	a, _ := New(root)
	b, _ := New(root)
	if _, err := a.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	cmds := []operations.Command{fupdate(t, "CMD-002", 2, "A wins"), fupdate(t, "CMD-003", 2, "B wins")}
	stores := []*Store{a, b}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range cmds {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = stores[i].Apply(context.Background(), cmds[i])
		}(i)
	}
	close(start)
	wg.Wait()
	success, conflict := 0, 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if anyCode(err) == "REVISION_CONFLICT" {
			conflict++
		} else {
			t.Fatalf("unexpected %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	state, err := a.Read(context.Background(), "PRD-001")
	if err != nil {
		t.Fatal(err)
	}
	if state.Document.Snapshot().Revision != 3 || len(state.Receipts) != 2 {
		t.Fatalf("lost CAS state %+v", state)
	}
}

func TestBatchFailureDoesNotPersist(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	svc := store
	if _, err := svc.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	before, err := store.Read(context.Background(), "PRD-001")
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, _ := before.Document.SemanticHash()
	title := "temporary"
	bad := operations.Command{Version: operations.CommandVersion, ID: "CMD-002", DocumentID: "PRD-001", ExpectedRevision: 2, Actor: operations.ActorContext{Class: "human", Subject: "local:test"}, Operations: []operations.Operation{
		{Kind: "update_node", NodeID: "REQ-001", Patch: &csd.NodePatch{Title: &title}},
		{Kind: "unlink", Edge: &csd.Edge{From: "REQ-001", Type: "missing", To: "REQ-001"}},
	}}
	if _, err = svc.Apply(context.Background(), bad); err == nil {
		t.Fatal("invalid batch committed")
	}
	after, err := store.Read(context.Background(), "PRD-001")
	if err != nil {
		t.Fatal(err)
	}
	afterHash, _ := after.Document.SemanticHash()
	if beforeHash != afterHash || after.Document.Snapshot().Revision != 2 || len(after.Receipts) != 1 {
		t.Fatal("partial batch persisted")
	}
}

func TestReplaceFailurePreservesOldState(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	svc := store
	if _, err := svc.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	path := store.statePath("PRD-001")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store.replace = func(string, string) error { return errors.New("injected replace failure") }
	_, err = svc.Apply(context.Background(), fupdate(t, "CMD-002", 2, "changed"))
	if anyCode(err) != "ATOMIC_REPLACE_FAILED" {
		t.Fatalf("got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("old state damaged")
	}
	entries, _ := os.ReadDir(store.docs)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Fatal("temp file leaked")
		}
	}
}

func TestPostReplaceSyncFailureIsReplayable(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	store.syncFolder = func(string) error { return errors.New("injected directory sync failure") }
	cmd := fcreate(t, "CMD-001")
	_, err := store.Apply(context.Background(), cmd)
	if anyCode(err) != "DIRECTORY_SYNC_FAILED" {
		t.Fatalf("got %v", err)
	}
	// Rename already committed the single envelope. The caller sees an ambiguous
	// durability error, then safely retries the same command ID instead of inventing
	// another command or assuming the write did not happen.
	store.syncFolder = syncDirectory
	result, err := store.Apply(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Document.Revision != 2 {
		t.Fatalf("ambiguous commit did not reconcile by replay: %+v", result)
	}
	state, err := store.Read(context.Background(), "PRD-001")
	if err != nil || len(state.Receipts) != 1 {
		t.Fatalf("receipt/document were not atomically co-committed: %v %+v", err, state)
	}
}

func TestStaleLockFailsClosed(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	lock := store.lockPath("PRD-001")
	if err := os.Mkdir(lock, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := store.Apply(ctx, fcreate(t, "CMD-001"))
	if anyCode(err) != "LOCK_TIMEOUT" {
		t.Fatalf("got %v", err)
	}
	if _, statErr := os.Stat(lock); statErr != nil {
		t.Fatal("stale lock was silently removed")
	}
	if _, statErr := os.Stat(store.statePath("PRD-001")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("state written while locked")
	}
}

func TestPathAndSymlinkDefenses(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	p := store.statePath("../../etc/passwd")
	rel, err := filepath.Rel(store.docs, p)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(filepath.Base(p), "..") {
		t.Fatalf("unsafe path %s", p)
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on many Windows hosts")
	}
	real := filepath.Join(root, "real")
	if err = os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err = os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if _, err = New(alias); anyCode(err) != "SYMLINK_REJECTED" {
		t.Fatalf("symlink root accepted: %v", err)
	}

	if _, err = store.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	statePath := store.statePath("PRD-001")
	if err = os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err = os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, statePath); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(context.Background(), "PRD-001")
	if anyCode(err) != "SYMLINK_REJECTED" {
		t.Fatalf("state symlink accepted: %v", err)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "secret" {
		t.Fatal("symlink target modified")
	}
}

func TestCorruptEnvelopeRejected(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	svc := store
	if _, err := svc.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	path := store.statePath("PRD-001")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(b), `"format":"prd-filestore/v1"`, `"format":"prd-filestore/v1","format":"prd-filestore/v1"`, 1)
	if err = os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(context.Background(), "PRD-001")
	if anyCode(err) != "DUPLICATE_KEY" {
		t.Fatalf("got %v", err)
	}
}

func TestHelperProcessApply(t *testing.T) {
	if os.Getenv("PRDH_HELPER") != "1" {
		return
	}
	root := os.Getenv("PRDH_ROOT")
	raw, err := base64.StdEncoding.DecodeString(os.Getenv("PRDH_COMMAND"))
	if err != nil {
		os.Exit(10)
	}
	var cmd operations.Command
	if err = json.Unmarshal(raw, &cmd); err != nil {
		os.Exit(11)
	}
	store, err := New(root)
	if err != nil {
		os.Exit(12)
	}
	_, err = store.Apply(context.Background(), cmd)
	if err == nil {
		os.Exit(0)
	}
	if anyCode(err) == "REVISION_CONFLICT" {
		os.Exit(3)
	}
	os.Exit(13)
}

func TestCrossProcessCAS(t *testing.T) {
	root := t.TempDir()
	store, _ := New(root)
	if _, err := store.Apply(context.Background(), fcreate(t, "CMD-001")); err != nil {
		t.Fatal(err)
	}
	cmds := []operations.Command{fupdate(t, "CMD-P1", 2, "process one"), fupdate(t, "CMD-P2", 2, "process two")}
	procs := make([]*exec.Cmd, 2)
	for i, c := range cmds {
		b, _ := json.Marshal(c)
		p := exec.Command(os.Args[0], "-test.run=^TestHelperProcessApply$")
		p.Env = append(os.Environ(), "PRDH_HELPER=1", "PRDH_ROOT="+root, "PRDH_COMMAND="+base64.StdEncoding.EncodeToString(b))
		procs[i] = p
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
	}
	codes := make([]int, 2)
	for i, p := range procs {
		err := p.Wait()
		if err == nil {
			codes[i] = 0
			continue
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		codes[i] = ee.ExitCode()
	}
	success, conflict := 0, 0
	for _, c := range codes {
		if c == 0 {
			success++
		} else if c == 3 {
			conflict++
		} else {
			t.Fatalf("helper exit codes %v", codes)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("helper exit codes %v", codes)
	}
	state, err := store.Read(context.Background(), "PRD-001")
	if err != nil {
		t.Fatal(err)
	}
	if state.Document.Snapshot().Revision != 3 || len(state.Receipts) != 2 {
		t.Fatal("cross-process CAS failed")
	}
}
