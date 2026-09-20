// Package filestore persists CSD current state and command receipts as one
// atomically replaced envelope. It does not decide semantic operations.
package filestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hvritual/PrdHarness/internal/csd"
	"github.com/hvritual/PrdHarness/internal/operations"
)

const FormatVersion = "prd-filestore/v1"
const MaxStateBytes = 16 << 20
const MaxReceipts = 100000

type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + " at " + e.Path + ": " + e.Message }
func problem(code, path, message string) error {
	return &Error{Code: code, Path: path, Message: message}
}

type Store struct {
	root       string
	docs       string
	locks      string
	retry      time.Duration
	replace    func(string, string) error
	syncFolder func(string) error
	engine     *operations.Engine
}

type envelope struct {
	Format   string               `json:"format"`
	Document json.RawMessage      `json:"document"`
	Receipts []operations.Receipt `json:"receipts"`
}

func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, problem("INVALID_STORE", "$", "store root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = ensureDirectory(abs, true); err != nil {
		return nil, err
	}
	docs := filepath.Join(abs, "documents")
	locks := filepath.Join(abs, "locks")
	if err = ensureDirectory(docs, true); err != nil {
		return nil, err
	}
	if err = ensureDirectory(locks, true); err != nil {
		return nil, err
	}
	return &Store{root: abs, docs: docs, locks: locks, retry: 10 * time.Millisecond, replace: atomicReplace, syncFolder: syncDirectory, engine: operations.NewEngine()}, nil
}

func ensureDirectory(path string, create bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || !create {
			return err
		}
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
		if err != nil {
			return err
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return problem("SYMLINK_REJECTED", path, "store directories cannot be symlinks")
	}
	if !info.IsDir() {
		return problem("INVALID_STORE", path, "expected directory")
	}
	return nil
}

func key(documentID string) string {
	sum := sha256.Sum256([]byte(documentID))
	return hex.EncodeToString(sum[:])
}
func (s *Store) statePath(documentID string) string {
	return filepath.Join(s.docs, key(documentID)+".json")
}
func (s *Store) lockPath(documentID string) string {
	return filepath.Join(s.locks, key(documentID)+".lock")
}

func (s *Store) Read(ctx context.Context, documentID string) (operations.State, error) {
	if err := ctx.Err(); err != nil {
		return operations.State{}, err
	}
	state, exists, err := s.load(documentID)
	if err != nil {
		return operations.State{}, err
	}
	if !exists {
		return operations.State{}, problem("NOT_FOUND", "$/document", documentID)
	}
	return state, nil
}

func (s *Store) Apply(ctx context.Context, cmd operations.Command) (result operations.Result, err error) {
	if s == nil || s.engine == nil {
		return operations.Result{}, problem("INVALID_STORE", "$", "store is required")
	}
	if err = cmd.Validate(); err != nil {
		return operations.Result{}, err
	}
	lock, err := s.acquire(ctx, cmd.DocumentID)
	if err != nil {
		return operations.Result{}, err
	}
	defer func() {
		if releaseErr := lock.release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	current, _, err := s.load(cmd.DocumentID)
	if err != nil {
		return operations.Result{}, err
	}
	transition, err := s.engine.Apply(current, cmd)
	if err != nil {
		return operations.Result{}, err
	}
	if !transition.Commit {
		return transition.Result, nil
	}
	if err = validateState(cmd.DocumentID, transition.State); err != nil {
		return operations.Result{}, err
	}
	data, err := encodeState(transition.State)
	if err != nil {
		return operations.Result{}, err
	}
	if err = s.writeAtomic(s.statePath(cmd.DocumentID), data); err != nil {
		return operations.Result{}, err
	}
	return transition.Result, nil
}

func validateState(documentID string, state operations.State) error {
	if state.Document == nil {
		return problem("CORRUPT_STATE", "$/document", "current document is required")
	}
	if err := state.Document.Validate(); err != nil {
		return err
	}
	snap := state.Document.Snapshot()
	if snap.ID != documentID {
		return problem("IDENTITY_MISMATCH", "$/document/id", "state path and document ID disagree")
	}
	if len(state.Receipts) == 0 || len(state.Receipts) > MaxReceipts {
		return problem("CORRUPT_STATE", "$/receipts", "requires 1..100000 receipts")
	}
	seen := map[string]bool{}
	previousRevision := int64(0)
	previousHash := ""
	for i, r := range state.Receipts {
		path := fmt.Sprintf("$/receipts/%d", i)
		if r.Version != operations.ReceiptVersion || r.CommandID == "" || r.CommandHash == "" || r.DocumentID != documentID {
			return problem("CORRUPT_STATE", path, "invalid receipt identity")
		}
		if seen[r.CommandID] {
			return problem("CORRUPT_STATE", path+"/command_id", "duplicate command ID")
		}
		seen[r.CommandID] = true
		if r.BaseRevision != previousRevision || r.ResultRevision <= r.BaseRevision {
			return problem("CORRUPT_STATE", path, "broken revision chain")
		}
		if r.ResultRevision-r.BaseRevision != int64(r.OperationCount) {
			return problem("CORRUPT_STATE", path+"/operation_count", "operation count does not match revision delta")
		}
		if r.BeforeHash != previousHash {
			return problem("CORRUPT_STATE", path+"/before_hash", "broken semantic hash chain")
		}
		if r.AfterHash == "" || r.OperationCount < 1 || r.RecordedAt == "" || r.Actor.Class == "" || r.Actor.Subject == "" || r.Actor.IdentityAssurance == "" {
			return problem("CORRUPT_STATE", path, "incomplete receipt")
		}
		if _, err := time.Parse(time.RFC3339Nano, r.RecordedAt); err != nil {
			return problem("CORRUPT_STATE", path+"/recorded_at", "invalid timestamp")
		}
		previousRevision = r.ResultRevision
		previousHash = r.AfterHash
	}
	currentHash, err := state.Document.SemanticHash()
	if err != nil {
		return err
	}
	if previousRevision != snap.Revision || previousHash != currentHash {
		return problem("CORRUPT_STATE", "$/receipts", "receipt head does not bind current document")
	}
	return nil
}

func encodeState(state operations.State) ([]byte, error) {
	doc, err := state.Document.Serialize()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(envelope{Format: FormatVersion, Document: doc, Receipts: state.Receipts})
	if err != nil {
		return nil, err
	}
	if len(b) > MaxStateBytes {
		return nil, problem("LIMIT", "$", "store state exceeds 16 MiB")
	}
	return b, nil
}

func (s *Store) load(documentID string) (operations.State, bool, error) {
	if err := ensureDirectory(s.docs, false); err != nil {
		return operations.State{}, false, err
	}
	path := s.statePath(documentID)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return operations.State{}, false, nil
		}
		return operations.State{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return operations.State{}, false, problem("SYMLINK_REJECTED", path, "state file cannot be a symlink")
	}
	if !info.Mode().IsRegular() {
		return operations.State{}, false, problem("INVALID_STORE", path, "state must be a regular file")
	}
	if info.Size() > MaxStateBytes {
		return operations.State{}, false, problem("LIMIT", path, "store state exceeds 16 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return operations.State{}, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxStateBytes+1))
	if err != nil {
		return operations.State{}, false, err
	}
	if len(b) > MaxStateBytes {
		return operations.State{}, false, problem("LIMIT", path, "store state exceeds 16 MiB")
	}
	state, err := decodeState(documentID, b)
	if err != nil {
		return operations.State{}, false, err
	}
	return state, true, nil
}

func decodeState(documentID string, b []byte) (operations.State, error) {
	if err := validateJSON(b); err != nil {
		return operations.State{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var env envelope
	if err := dec.Decode(&env); err != nil {
		return operations.State{}, problem("CORRUPT_STATE", "$", err.Error())
	}
	if env.Format != FormatVersion {
		return operations.State{}, problem("UNSUPPORTED_STORE", "$/format", env.Format)
	}
	if len(env.Document) == 0 {
		return operations.State{}, problem("CORRUPT_STATE", "$/document", "missing document")
	}
	doc, err := csd.Load(env.Document)
	if err != nil {
		return operations.State{}, err
	}
	state := operations.State{Document: doc, Receipts: append([]operations.Receipt(nil), env.Receipts...)}
	if err = validateState(documentID, state); err != nil {
		return operations.State{}, err
	}
	return state, nil
}

func (s *Store) writeAtomic(path string, b []byte) error {
	if len(b) > MaxStateBytes {
		return problem("LIMIT", "$", "store state exceeds 16 MiB")
	}
	if err := ensureDirectory(s.docs, false); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return problem("SYMLINK_REJECTED", path, "state file cannot be a symlink")
		}
		if !info.Mode().IsRegular() {
			return problem("INVALID_STORE", path, "state must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(s.docs, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }
	defer cleanup()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	n, err := f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = s.replace(tmp, path); err != nil {
		return problem("ATOMIC_REPLACE_FAILED", path, err.Error())
	}
	if err = s.syncFolder(s.docs); err != nil {
		return problem("DIRECTORY_SYNC_FAILED", s.docs, err.Error())
	}
	return nil
}

type heldLock struct{ path, owner, token string }

func (s *Store) acquire(ctx context.Context, documentID string) (*heldLock, error) {
	if err := ensureDirectory(s.locks, false); err != nil {
		return nil, err
	}
	path := s.lockPath(documentID)
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)
	for {
		if err := ctx.Err(); err != nil {
			return nil, problem("LOCK_TIMEOUT", path, err.Error())
		}
		err := os.Mkdir(path, 0700)
		if err == nil {
			owner := filepath.Join(path, "owner.json")
			payload, _ := json.Marshal(map[string]any{"token": token, "pid": os.Getpid(), "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
			if writeErr := os.WriteFile(owner, payload, 0600); writeErr != nil {
				_ = os.Remove(path)
				return nil, writeErr
			}
			return &heldLock{path: path, owner: owner, token: token}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, lerr := os.Lstat(path)
		if lerr != nil && !errors.Is(lerr, os.ErrNotExist) {
			return nil, lerr
		}
		if lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, problem("SYMLINK_REJECTED", path, "lock cannot be a symlink")
		}
		if lerr == nil && !info.IsDir() {
			return nil, problem("INVALID_LOCK", path, "lock path must be a directory")
		}
		timer := time.NewTimer(s.retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, problem("LOCK_TIMEOUT", path, ctx.Err().Error())
		case <-timer.C:
		}
	}
}

func (l *heldLock) release() error {
	if l == nil {
		return nil
	}
	b, err := os.ReadFile(l.owner)
	if err != nil {
		return problem("LOCK_RELEASE_FAILED", l.path, err.Error())
	}
	var meta struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(b, &meta); err != nil || meta.Token != l.token {
		return problem("LOCK_RELEASE_FAILED", l.path, "lock ownership changed")
	}
	if err = os.Remove(l.owner); err != nil {
		return err
	}
	if err = os.Remove(l.path); err != nil {
		return err
	}
	return nil
}
