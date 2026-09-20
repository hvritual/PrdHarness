package csd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

func Load(data []byte) (*Document, error) {
	v, err := strictJSON(data)
	if err != nil {
		return nil, err
	}
	if err := shapeSnapshot(v); err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, problem("INVALID_TYPE", "$", err.Error())
	}
	return FromSnapshot(s)
}

func (d *Document) normalized() (Snapshot, error) {
	if err := d.Validate(); err != nil {
		return Snapshot{}, err
	}
	s := d.Snapshot()
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return edgeKey(s.Edges[i]) < edgeKey(s.Edges[j]) })
	sort.Strings(s.Presentation.Collapsed)
	return s, nil
}

func edgeKey(e Edge) string { return e.From + "\x00" + e.Type + "\x00" + e.To }

// Serialize returns compact deterministic JSON, without a trailing newline.
func (d *Document) Serialize() ([]byte, error) {
	s, err := d.normalized()
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// semanticProjection defines the complete, versioned meaning of SemanticHash.
// This is not RFC 8785/JCS and does not infer equivalent natural-language text.
type semanticProjection struct {
	Schema      string `json:"schema"`
	HashVersion string `json:"hash_version"`
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Nodes       []Node `json:"nodes"`
	Edges       []Edge `json:"edges"`
}

func (d *Document) projection() (semanticProjection, error) {
	s, err := d.normalized()
	if err != nil {
		return semanticProjection{}, err
	}
	return semanticProjection{s.Schema, HashVersion, s.ID, s.Type, s.Title, s.Nodes, s.Edges}, nil
}

// SemanticBytes exposes the exact digest input for independent verification.
func (d *Document) SemanticBytes() ([]byte, error) {
	p, err := d.projection()
	if err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func (d *Document) SemanticHash() (string, error) {
	b, err := d.SemanticBytes()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:sha256:%x", HashVersion, sha256.Sum256(b)), nil
}
