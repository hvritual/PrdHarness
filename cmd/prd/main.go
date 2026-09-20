// prd exposes the CSD core as small, composable commands. It is not a file store.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hvritual/PrdHarness/internal/csd"
)

const help = `Usage: prd <command> [flags]
Commands:
  create        --id ID --type TYPE --title TITLE
  add-node      --file CSD --node NODE.json
  update-node   --file CSD --id ID --patch PATCH.json
  link-node     --file CSD --from ID --type RELATION --to ID
  validate      --file CSD
  serialize     --file CSD
  load          --file CSD
  semantic-hash --file CSD
  semantic-diff --before CSD --after CSD
All commands support --out NEW_FILE (never overwrites an existing file).
Without --out, results go to stdout. Never redirect stdout over an input file.
Exit codes: 0 success (including a nonempty diff), 1 input/operation error, 2 usage.
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
	var file, id, kind, title, node, patch, from, to, before, after string
	output := f.String("out", "", "new output file")
	switch command {
	case "create":
		f.StringVar(&id, "id", "", "document ID")
		f.StringVar(&kind, "type", "", "document type")
		f.StringVar(&title, "title", "", "document title")
	case "add-node", "update-node", "link-node", "validate", "serialize", "load", "semantic-hash":
		f.StringVar(&file, "file", "", "input document")
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
	if err := writeResult(*output, b, out); err != nil {
		return failure(errOut, err, 1)
	}
	return 0
}

func failure(w io.Writer, err error, exit int) int {
	var detail *csd.Error
	if !errors.As(err, &detail) {
		detail = &csd.Error{Code: "COMMAND_ERROR", Path: "$", Message: err.Error()}
	}
	_ = json.NewEncoder(w).Encode(detail)
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
	// Exclusive creation protects every existing file, including the input snapshot.
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
