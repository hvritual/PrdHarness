// Package operations provides the only supported mutation protocol for a
// persisted CSD. It is intentionally independent from file-system details.
package operations

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hvritual/PrdHarness/internal/csd"
)

const CommandVersion = "semantic-command/v1"
const ReceiptVersion = "semantic-receipt/v1"
const DeprecationField = "csd_deprecated"

const MaxOperations = 100

var classPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + " at " + e.Path + ": " + e.Message }
func problem(code, path, message string) error {
	return &Error{Code: code, Path: path, Message: message}
}

type ActorContext struct {
	Class   string `json:"class"`
	Subject string `json:"subject"`
}

type DocumentInput struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

type Operation struct {
	Kind     string         `json:"kind"`
	Document *DocumentInput `json:"document,omitempty"`
	Node     *csd.Node      `json:"node,omitempty"`
	NodeID   string         `json:"node_id,omitempty"`
	Patch    *csd.NodePatch `json:"patch,omitempty"`
	Edge     *csd.Edge      `json:"edge,omitempty"`
}

type Command struct {
	Version          string       `json:"version"`
	ID               string       `json:"id"`
	DocumentID       string       `json:"document_id"`
	ExpectedRevision int64        `json:"expected_revision"`
	Actor            ActorContext `json:"actor"`
	Operations       []Operation  `json:"operations"`
}

type RecordedActor struct {
	Class             string `json:"class"`
	Subject           string `json:"subject"`
	IdentityAssurance string `json:"identity_assurance"`
}

type Receipt struct {
	Version        string        `json:"version"`
	CommandID      string        `json:"command_id"`
	CommandHash    string        `json:"command_hash"`
	DocumentID     string        `json:"document_id"`
	BaseRevision   int64         `json:"base_revision"`
	ResultRevision int64         `json:"result_revision"`
	BeforeHash     string        `json:"before_hash,omitempty"`
	AfterHash      string        `json:"after_hash"`
	Actor          RecordedActor `json:"actor"`
	OperationCount int           `json:"operation_count"`
	RecordedAt     string        `json:"recorded_at"`
}

type State struct {
	Document *csd.Document
	Receipts []Receipt
}

func CloneState(s State) State {
	return State{Document: s.Document, Receipts: append([]Receipt(nil), s.Receipts...)}
}

type Transition struct {
	State  State
	Result Result
	Commit bool
}

type Result struct {
	Replayed bool         `json:"replayed"`
	Receipt  Receipt      `json:"receipt"`
	Document csd.Snapshot `json:"document"`
}

func validateActor(a ActorContext) error {
	if !classPattern.MatchString(a.Class) {
		return problem("INVALID_ACTOR", "$/actor/class", "expected lower_snake_case actor class")
	}
	switch a.Class {
	case "human", "ai", "system", "imported":
	default:
		return problem("INVALID_ACTOR", "$/actor/class", "unsupported actor class")
	}
	if !utf8.ValidString(a.Subject) || strings.TrimSpace(a.Subject) == "" || len(a.Subject) > 256 {
		return problem("INVALID_ACTOR", "$/actor/subject", "nonempty UTF-8 subject up to 256 bytes required")
	}
	return nil
}

func (c Command) Validate() error {
	if c.Version != CommandVersion {
		return problem("UNSUPPORTED_COMMAND", "$/version", c.Version)
	}
	if !csd.ValidID(c.ID) {
		return problem("INVALID_COMMAND_ID", "$/id", "invalid command ID")
	}
	if !csd.ValidID(c.DocumentID) {
		return problem("INVALID_DOCUMENT_ID", "$/document_id", "invalid document ID")
	}
	if c.ExpectedRevision < 0 {
		return problem("INVALID_REVISION", "$/expected_revision", "must be zero or positive")
	}
	if err := validateActor(c.Actor); err != nil {
		return err
	}
	if len(c.Operations) == 0 || len(c.Operations) > MaxOperations {
		return problem("INVALID_OPERATIONS", "$/operations", "requires 1..100 operations")
	}
	for i, op := range c.Operations {
		if err := validateOperation(op); err != nil {
			if e, ok := err.(*Error); ok {
				e.Path = "$/operations/" + jsonPointerIndex(i) + strings.TrimPrefix(e.Path, "$")
			}
			return err
		}
	}
	return nil
}

func jsonPointerIndex(i int) string { return strconv.Itoa(i) }

func only(op Operation, allowed ...string) error {
	present := map[string]bool{
		"document": op.Document != nil,
		"node":     op.Node != nil,
		"node_id":  op.NodeID != "",
		"patch":    op.Patch != nil,
		"edge":     op.Edge != nil,
	}
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	for field, has := range present {
		if has && !ok[field] {
			return problem("INVALID_OPERATION", "$/"+field, "field not allowed for "+op.Kind)
		}
	}
	for _, field := range allowed {
		if !present[field] {
			return problem("INVALID_OPERATION", "$/"+field, "field required for "+op.Kind)
		}
	}
	return nil
}

func validateOperation(op Operation) error {
	switch op.Kind {
	case "create_document":
		return only(op, "document")
	case "add_node":
		return only(op, "node")
	case "update_node":
		if err := only(op, "node_id", "patch"); err != nil {
			return err
		}
		if op.Patch.Title == nil && len(op.Patch.SetFields) == 0 && len(op.Patch.RemoveFields) == 0 {
			return problem("NO_CHANGE", "$/patch", "empty update is not a semantic operation")
		}
		return nil
	case "link", "unlink":
		return only(op, "edge")
	case "deprecate_node":
		return only(op, "node_id")
	default:
		return problem("UNKNOWN_OPERATION", "$/kind", op.Kind)
	}
}
