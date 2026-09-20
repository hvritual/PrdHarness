package operations

import (
	"bytes"
	"fmt"
	"time"

	"github.com/hvritual/PrdHarness/internal/csd"
)

// Engine is a pure semantic command state machine. It does not perform IO or
// locking; repositories must execute it while holding their commit boundary.
type Engine struct{ now func() time.Time }

func NewEngine() *Engine { return &Engine{now: time.Now} }

func (e *Engine) Apply(current State, cmd Command) (Transition, error) {
	if e == nil {
		return Transition{}, problem("INVALID_ENGINE", "$", "engine is required")
	}
	if err := cmd.Validate(); err != nil {
		return Transition{}, err
	}
	commandHash, err := CommandHash(cmd)
	if err != nil {
		return Transition{}, err
	}
	current = CloneState(current)

	for _, receipt := range current.Receipts {
		if receipt.CommandID != cmd.ID {
			continue
		}
		if receipt.CommandHash != commandHash {
			return Transition{}, problem("IDEMPOTENCY_CONFLICT", "$/id", "command ID was already used with different content")
		}
		if current.Document == nil {
			return Transition{}, problem("CORRUPT_STATE", "$", "receipt exists without a current document")
		}
		return Transition{State: current, Result: Result{Replayed: true, Receipt: receipt, Document: current.Document.Snapshot()}, Commit: false}, nil
	}

	baseRevision := int64(0)
	beforeHash := ""
	if current.Document != nil {
		baseRevision = current.Document.Snapshot().Revision
		beforeHash, err = current.Document.SemanticHash()
		if err != nil {
			return Transition{}, err
		}
	}
	if cmd.ExpectedRevision != baseRevision {
		return Transition{}, problem("REVISION_CONFLICT", "$/expected_revision", fmt.Sprintf("expected %d, current %d", cmd.ExpectedRevision, baseRevision))
	}

	next := current.Document
	for i, op := range cmd.Operations {
		next, err = applyOperation(next, cmd.DocumentID, op)
		if err != nil {
			return Transition{}, fmt.Errorf("operation %d (%s): %w", i, op.Kind, err)
		}
	}
	if next == nil {
		return Transition{}, problem("INVALID_OPERATIONS", "$/operations", "command produced no document")
	}
	if err = next.Validate(); err != nil {
		return Transition{}, err
	}
	afterHash, err := next.SemanticHash()
	if err != nil {
		return Transition{}, err
	}
	if beforeHash != "" && afterHash == beforeHash {
		return Transition{}, problem("NO_SEMANTIC_CHANGE", "$/operations", "command does not change semantic content")
	}

	receipt := Receipt{
		Version: ReceiptVersion, CommandID: cmd.ID, CommandHash: commandHash, DocumentID: cmd.DocumentID,
		BaseRevision: baseRevision, ResultRevision: next.Snapshot().Revision, BeforeHash: beforeHash, AfterHash: afterHash,
		Actor:          RecordedActor{Class: cmd.Actor.Class, Subject: cmd.Actor.Subject, IdentityAssurance: "caller_asserted_local"},
		OperationCount: len(cmd.Operations), RecordedAt: e.now().UTC().Format(time.RFC3339Nano),
	}
	nextState := CloneState(current)
	nextState.Document = next
	nextState.Receipts = append(nextState.Receipts, receipt)
	return Transition{State: nextState, Result: Result{Receipt: receipt, Document: next.Snapshot()}, Commit: true}, nil
}

func applyOperation(d *csd.Document, documentID string, op Operation) (*csd.Document, error) {
	switch op.Kind {
	case "create_document":
		if d != nil {
			return nil, problem("ALREADY_EXISTS", "$/document", "document already exists")
		}
		return csd.NewDocument(documentID, op.Document.Type, op.Document.Title)
	case "add_node":
		if d == nil {
			return nil, problem("NOT_FOUND", "$/document", "document does not exist")
		}
		return d.AddNode(*op.Node)
	case "update_node":
		if d == nil {
			return nil, problem("NOT_FOUND", "$/document", "document does not exist")
		}
		return d.UpdateNode(op.NodeID, *op.Patch)
	case "link":
		if d == nil {
			return nil, problem("NOT_FOUND", "$/document", "document does not exist")
		}
		return d.Link(*op.Edge)
	case "unlink":
		if d == nil {
			return nil, problem("NOT_FOUND", "$/document", "document does not exist")
		}
		return d.Unlink(*op.Edge)
	case "deprecate_node":
		if d == nil {
			return nil, problem("NOT_FOUND", "$/document", "document does not exist")
		}
		found := false
		for _, n := range d.Snapshot().Nodes {
			if n.ID != op.NodeID {
				continue
			}
			found = true
			if v, ok := n.Fields[DeprecationField]; ok {
				b, marshalErr := v.MarshalJSON()
				if marshalErr != nil {
					return nil, marshalErr
				}
				if bytes.Equal(b, []byte("true")) {
					return nil, problem("ALREADY_DEPRECATED", "$/node_id", op.NodeID)
				}
				if !bytes.Equal(b, []byte("false")) {
					return nil, problem("INVALID_DEPRECATION_STATE", "$/node_id", "reserved deprecation field must be boolean")
				}
			}
			break
		}
		if !found {
			return nil, problem("NOT_FOUND", "$/node_id", op.NodeID)
		}
		v, err := csd.ParseValue([]byte("true"))
		if err != nil {
			return nil, err
		}
		return d.UpdateNode(op.NodeID, csd.NodePatch{SetFields: map[string]csd.Value{DeprecationField: v}})
	default:
		return nil, problem("UNKNOWN_OPERATION", "$/kind", op.Kind)
	}
}
