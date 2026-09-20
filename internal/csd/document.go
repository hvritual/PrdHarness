// Package csd implements immutable semantic document snapshots. It has no IO,
// model provider, business policy, approval authority or global mutable state.
package csd

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = "csd/v1"
const HashVersion = "csd-semantic/v1"

var idPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:-]{0,127}$`)
var typePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Node struct {
	ID     string           `json:"id"`
	Type   string           `json:"type"`
	Title  string           `json:"title"`
	Fields map[string]Value `json:"fields"`
}

type Edge struct {
	From string `json:"from"`
	Type string `json:"type"`
	To   string `json:"to"`
}

// Presentation contains only non-normative UI state. Workflow order belongs in Fields.
type Presentation struct {
	NodeOrder []string `json:"node_order"`
	Collapsed []string `json:"collapsed"`
}

type Snapshot struct {
	Schema       string       `json:"schema"`
	ID           string       `json:"id"`
	Type         string       `json:"type"`
	Title        string       `json:"title"`
	Revision     int64        `json:"revision"`
	Nodes        []Node       `json:"nodes"`
	Edges        []Edge       `json:"edges"`
	Presentation Presentation `json:"presentation"`
}

type Document struct{ state Snapshot }

type NodePatch struct {
	Title        *string          `json:"title,omitempty"`
	SetFields    map[string]Value `json:"set_fields,omitempty"`
	RemoveFields []string         `json:"remove_fields,omitempty"`
}

func NewDocument(id, kind, title string) (*Document, error) {
	return FromSnapshot(Snapshot{Schema: SchemaVersion, ID: id, Type: kind, Title: title, Revision: 1})
}

// FromSnapshot imports data, not authority. Caller-supplied revision is not a CAS receipt.
func FromSnapshot(s Snapshot) (*Document, error) {
	d := &Document{state: cloneSnapshot(s)}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Document) Snapshot() Snapshot { return cloneSnapshot(d.state) }

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func cloneNode(n Node) Node {
	fields := make(map[string]Value, len(n.Fields))
	for k, v := range n.Fields {
		fields[k] = v
	}
	n.Fields = fields
	return n
}

func cloneSnapshot(s Snapshot) Snapshot {
	nodes := make([]Node, len(s.Nodes))
	for i, n := range s.Nodes {
		nodes[i] = cloneNode(n)
	}
	s.Nodes = nodes
	s.Edges = append([]Edge{}, s.Edges...)
	s.Presentation.NodeOrder = append([]string{}, s.Presentation.NodeOrder...)
	s.Presentation.Collapsed = append([]string{}, s.Presentation.Collapsed...)
	return s
}

func validateNode(n Node, path string) error {
	if !idPattern.MatchString(n.ID) {
		return problem("INVALID_ID", path+"/id", "invalid stable ID")
	}
	if !typePattern.MatchString(n.Type) {
		return problem("INVALID_TYPE", path+"/type", "invalid node type")
	}
	if !utf8.ValidString(n.Title) || strings.TrimSpace(n.Title) == "" {
		return problem("INVALID_TITLE", path+"/title", "nonempty UTF-8 title required")
	}
	for _, k := range keys(n.Fields) {
		if !typePattern.MatchString(k) {
			return problem("INVALID_FIELD", path+"/fields/"+k, "expected lower_snake_case key")
		}
		if n.Fields[k].raw == "" {
			return problem("INVALID_VALUE", path+"/fields/"+k, "uninitialized Value")
		}
	}
	return nil
}

func (d *Document) Validate() error {
	if d == nil {
		return problem("INVALID_DOCUMENT", "$", "nil document")
	}
	s := d.state
	if s.Schema != SchemaVersion {
		return problem("UNSUPPORTED_SCHEMA", "$/schema", s.Schema)
	}
	if !idPattern.MatchString(s.ID) {
		return problem("INVALID_ID", "$/id", "invalid stable ID")
	}
	if !typePattern.MatchString(s.Type) {
		return problem("INVALID_TYPE", "$/type", "invalid document type")
	}
	if !utf8.ValidString(s.Title) || strings.TrimSpace(s.Title) == "" {
		return problem("INVALID_TITLE", "$/title", "nonempty UTF-8 title required")
	}
	if s.Revision < 1 {
		return problem("INVALID_REVISION", "$/revision", "must be positive")
	}
	if len(s.Nodes) > 10000 || len(s.Edges) > 50000 {
		return problem("LIMIT", "$", "too many nodes or edges")
	}
	seen := make(map[string]bool)
	for _, n := range s.Nodes {
		if err := validateNode(n, "$/nodes/"+n.ID); err != nil {
			return err
		}
		if seen[n.ID] {
			return problem("DUPLICATE_ID", "$/nodes/"+n.ID, "node already exists")
		}
		seen[n.ID] = true
	}
	edges := make(map[Edge]bool)
	for _, e := range s.Edges {
		if !typePattern.MatchString(e.Type) {
			return problem("INVALID_TYPE", "$/edges", "invalid edge type")
		}
		if !seen[e.From] || !seen[e.To] {
			return problem("DANGLING_EDGE", "$/edges", e.From+" -> "+e.To)
		}
		if edges[e] {
			return problem("DUPLICATE_EDGE", "$/edges", e.From+" -> "+e.To)
		}
		edges[e] = true
	}
	for _, list := range [][]string{s.Presentation.NodeOrder, s.Presentation.Collapsed} {
		used := make(map[string]bool)
		for _, id := range list {
			if !seen[id] || used[id] {
				return problem("INVALID_PRESENTATION", "$/presentation", "unknown or duplicate node: "+id)
			}
			used[id] = true
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Check the whole wire document, not just each standalone field Value.
	// Otherwise an accepted Value could exceed the loader's depth limit once embedded.
	_, err = strictJSON(b)
	return err
}

func (d *Document) next() (Snapshot, error) {
	if err := d.Validate(); err != nil {
		return Snapshot{}, err
	}
	if d.state.Revision == math.MaxInt64 {
		return Snapshot{}, problem("REVISION_OVERFLOW", "$/revision", "cannot increment")
	}
	s := d.Snapshot()
	s.Revision++
	return s, nil
}

func (d *Document) AddNode(n Node) (*Document, error) {
	s, err := d.next()
	if err != nil {
		return nil, err
	}
	s.Nodes = append(s.Nodes, cloneNode(n))
	return FromSnapshot(s)
}

func (d *Document) UpdateNode(id string, p NodePatch) (*Document, error) {
	s, err := d.next()
	if err != nil {
		return nil, err
	}
	for i := range s.Nodes {
		if s.Nodes[i].ID != id {
			continue
		}
		n := &s.Nodes[i]
		if p.Title != nil {
			n.Title = *p.Title
		}
		removed := make(map[string]bool)
		for _, k := range p.RemoveFields {
			if removed[k] {
				return nil, problem("INVALID_PATCH", "$/remove_fields", "duplicate field: "+k)
			}
			if _, ok := p.SetFields[k]; ok {
				return nil, problem("INVALID_PATCH", "$/fields/"+k, "cannot set and remove the same field")
			}
			if _, ok := n.Fields[k]; !ok {
				return nil, problem("NOT_FOUND", "$/fields/"+k, "cannot remove missing field")
			}
			removed[k] = true
			delete(n.Fields, k)
		}
		for k, v := range p.SetFields {
			n.Fields[k] = v
		}
		return FromSnapshot(s)
	}
	return nil, problem("NOT_FOUND", "$/nodes/"+id, "node does not exist")
}

func (d *Document) Link(e Edge) (*Document, error) {
	s, err := d.next()
	if err != nil {
		return nil, err
	}
	s.Edges = append(s.Edges, e)
	return FromSnapshot(s)
}
