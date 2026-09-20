package csd

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

type Change struct {
	Kind   string          `json:"kind"`
	Path   string          `json:"path"`
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
}

type Diff struct {
	FromHash string   `json:"from_hash"`
	ToHash   string   `json:"to_hash"`
	Changes  []Change `json:"changes"`
}

func pointer(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }

func SemanticDiff(before, after *Document) (Diff, error) {
	a, err := before.projection()
	if err != nil {
		return Diff{}, err
	}
	b, err := after.projection()
	if err != nil {
		return Diff{}, err
	}
	if a.ID != b.ID || a.Type != b.Type || a.Schema != b.Schema {
		return Diff{}, problem("IDENTITY_MISMATCH", "$", "compare revisions of the same document and schema")
	}
	from, err := before.SemanticHash()
	if err != nil {
		return Diff{}, err
	}
	to, err := after.SemanticHash()
	if err != nil {
		return Diff{}, err
	}
	result := Diff{from, to, []Change{}}
	add := func(path string, x, y any, hasX, hasY bool) {
		var old, next []byte
		if hasX {
			old, _ = json.Marshal(x)
		}
		if hasY {
			next, _ = json.Marshal(y)
		}
		if hasX == hasY && bytes.Equal(old, next) {
			return
		}
		kind := "replace"
		if !hasX {
			kind = "add"
		}
		if !hasY {
			kind = "remove"
		}
		result.Changes = append(result.Changes, Change{kind, path, old, next})
	}
	add("/title", a.Title, b.Title, true, true)
	left, right := make(map[string]Node), make(map[string]Node)
	ids := make(map[string]bool)
	for _, n := range a.Nodes {
		left[n.ID] = n
		ids[n.ID] = true
	}
	for _, n := range b.Nodes {
		right[n.ID] = n
		ids[n.ID] = true
	}
	for _, id := range keys(ids) {
		x, hasX := left[id]
		y, hasY := right[id]
		path := "/nodes/" + pointer(id)
		if !hasX || !hasY {
			add(path, x, y, hasX, hasY)
			continue
		}
		add(path+"/type", x.Type, y.Type, true, true)
		add(path+"/title", x.Title, y.Title, true, true)
		fields := make(map[string]bool)
		for k := range x.Fields {
			fields[k] = true
		}
		for k := range y.Fields {
			fields[k] = true
		}
		for _, k := range keys(fields) {
			v, vx := x.Fields[k]
			w, wy := y.Fields[k]
			add(path+"/fields/"+pointer(k), v, w, vx, wy)
		}
	}
	le, re := make(map[Edge]bool), make(map[Edge]bool)
	for _, e := range a.Edges {
		le[e] = true
	}
	for _, e := range b.Edges {
		re[e] = true
	}
	for e := range le {
		if !re[e] {
			add("/edges/"+pointer(e.From)+"/"+pointer(e.Type)+"/"+pointer(e.To), e, nil, true, false)
		}
	}
	for e := range re {
		if !le[e] {
			add("/edges/"+pointer(e.From)+"/"+pointer(e.Type)+"/"+pointer(e.To), nil, e, false, true)
		}
	}
	sort.Slice(result.Changes, func(i, j int) bool { return result.Changes[i].Path < result.Changes[j].Path })
	return result, nil
}
