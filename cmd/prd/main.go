// prd exposes detached CSD utilities plus the controlled persisted write path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hvritual/PrdHarness/internal/csd"
	"github.com/hvritual/PrdHarness/internal/filestore"
	"github.com/hvritual/PrdHarness/internal/operations"
)

const help = `Usage: prd <command> [flags]
Controlled store commands:
  apply         --store DIR --command COMMAND.json [--lock-timeout 5s]
  get           --store DIR --id ID
Detached snapshot utilities (do not mutate the canonical store):
  create        --id ID --type TYPE --title TITLE
  add-node      --file CSD --node NODE.json
  update-node   --file CSD --id ID --patch PATCH.json
  link-node     --file CSD --from ID --type RELATION --to ID
  validate      --file CSD
  serialize     --file CSD
  load          --file CSD
  semantic-hash --file CSD
  semantic-diff --before CSD --after CSD
Detached commands support --out NEW_FILE (never overwrite an existing file).
The canonical file store is written only through apply.
Exit codes: 0 success, 1 input/operation/store error, 2 usage.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		if _, err := io.WriteString(out, help); err != nil {
			return failure(errOut, err, 1)
		}
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	command := args[0]
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var file, id, kind, title, node, patch, from, to, before, after, storePath, commandPath string
	var output string
	lockTimeout := 5 * time.Second
	switch command {
	case "apply":
		f.StringVar(&storePath, "store", "", "canonical store directory")
		f.StringVar(&commandPath, "command", "", "semantic command JSON")
		f.DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "maximum time to wait for document lock")
	case "get":
		f.StringVar(&storePath, "store", "", "canonical store directory")
		f.StringVar(&id, "id", "", "document ID")
	case "create":
		f.StringVar(&id, "id", "", "document ID")
		f.StringVar(&kind, "type", "", "document type")
		f.StringVar(&title, "title", "", "document title")
		f.StringVar(&output, "out", "", "new output file")
	case "add-node", "update-node", "link-node", "validate", "serialize", "load", "semantic-hash":
		f.StringVar(&file, "file", "", "input document")
		f.StringVar(&output, "out", "", "new output file")
		if command == "add-node" {
			f.StringVar(&node, "node", "", "node JSON")
		}
		if command == "update-node" {
			f.StringVar(&id, "id", "", "node ID")
			f.StringVar(&patch, "patch", "", "patch JSON")
		}
		if command == "link-node" {
			f.StringVar(&from, "from", "", "source ID")
			f.StringVar(&to, "to", "", "target ID")
			f.StringVar(&kind, "type", "", "relation type")
		}
	case "semantic-diff":
		f.StringVar(&before, "before", "", "old document")
		f.StringVar(&after, "after", "", "new document")
		f.StringVar(&output, "out", "", "new output file")
	default:
		return failure(errOut, fmt.Errorf("unknown command %q; use help", command), 2)
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(out, help)
			return 0
		}
		return failure(errOut, err, 2)
	}
	if f.NArg() != 0 {
		return failure(errOut, errors.New("unexpected positional arguments"), 2)
	}
	if command == "apply" {
		if lockTimeout <= 0 {
			return failure(errOut, errors.New("lock-timeout must be positive"), 2)
		}
		b, err := readLimited(commandPath)
		if err != nil {
			return failure(errOut, err, 1)
		}
		cmd, err := operations.DecodeCommand(b)
		if err != nil {
			return failure(errOut, err, 1)
		}
		store, err := filestore.New(storePath)
		if err != nil {
			return failure(errOut, err, 1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
		defer cancel()
		result, err := store.Apply(ctx, cmd)
		if err != nil {
			return failure(errOut, err, 1)
		}
		payload, err := json.Marshal(result)
		if err != nil {
			return failure(errOut, err, 1)
		}
		payload = append(payload, '\n')
		if _, err = out.Write(payload); err != nil {
			return failure(errOut, err, 1)
		}
		return 0
	}
	if command == "get" {
		store, err := filestore.New(storePath)
		if err != nil {
			return failure(errOut, err, 1)
		}
		state, err := store.Read(context.Background(), id)
		if err != nil {
			return failure(errOut, err, 1)
		}
		b, err := state.Document.Serialize()
		if err != nil {
			return failure(errOut, err, 1)
		}
		b = append(b, '\n')
		if _, err = out.Write(b); err != nil {
			return failure(errOut, err, 1)
		}
		return 0
	}

	var result any
	var d *csd.Document
	var err error
	switch command {
	case "create":
		d, err = csd.NewDocument(id, kind, title)
	case "semantic-diff":
		var old, next *csd.Document
		old, err = readDocument(before)
		if err == nil {
			next, err = readDocument(after)
		}
		if err == nil {
			result, err = csd.SemanticDiff(old, next)
		}
	default:
		d, err = readDocument(file)
		if err != nil {
			break
		}
		switch command {
		case "add-node":
			var b []byte
			var n csd.Node
			b, err = readLimited(node)
			if err == nil {
				n, err = csd.DecodeNode(b)
			}
			if err == nil {
				d, err = d.AddNode(n)
			}
		case "update-node":
			var b []byte
			var p csd.NodePatch
			b, err = readLimited(patch)
			if err == nil {
				p, err = csd.DecodePatch(b)
			}
			if err == nil {
				d, err = d.UpdateNode(id, p)
			}
		case "link-node":
			d, err = d.Link(csd.Edge{From: from, Type: kind, To: to})
		case "validate":
			err = d.Validate()
			result = map[string]any{"status": "VALID", "document": d.Snapshot().ID, "revision": d.Snapshot().Revision}
		case "semantic-hash":
			var hash string
			hash, err = d.SemanticHash()
			result = map[string]string{"semantic_hash": hash}
		}
	}
	if err != nil {
		return failure(errOut, err, 1)
	}
	var b []byte
	if result != nil {
		b, err = json.Marshal(result)
	} else {
		b, err = d.Serialize()
	}
	if err != nil {
		return failure(errOut, err, 1)
	}
	b = append(b, '\n')
	if err = writeResult(output, b, out); err != nil {
		return failure(errOut, err, 1)
	}
	return 0
}

func failure(w io.Writer, err error, exit int) int {
	var ce *csd.Error
	if errors.As(err, &ce) {
		_ = json.NewEncoder(w).Encode(ce)
		return exit
	}
	var oe *operations.Error
	if errors.As(err, &oe) {
		_ = json.NewEncoder(w).Encode(oe)
		return exit
	}
	var fe *filestore.Error
	if errors.As(err, &fe) {
		_ = json.NewEncoder(w).Encode(fe)
		return exit
	}
	_ = json.NewEncoder(w).Encode(&csd.Error{Code: "COMMAND_ERROR", Path: "$", Message: err.Error()})
	return exit
}

func readLimited(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("input path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, csd.MaxJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > csd.MaxJSONBytes {
		return nil, errors.New("input exceeds 4 MiB")
	}
	return b, nil
}
func readDocument(path string) (*csd.Document, error) {
	b, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	return csd.Load(b)
}
func writeResult(path string, b []byte, stdout io.Writer) error {
	if path == "" {
		n, err := stdout.Write(b)
		if err == nil && n != len(b) {
			return io.ErrShortWrite
		}
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, writeErr := f.Write(b)
	closeErr := f.Close()
	if writeErr == nil && n != len(b) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}
	return nil
}
