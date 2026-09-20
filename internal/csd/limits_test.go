package csd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hvritual/PrdHarness/internal/csd"
)

func TestEmbeddedValueDepthBound(t *testing.T) {
	base := fresh(t)
	before := serialize(t, base)
	for _, depth := range []int{60, 61, 64} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		value := val(t, raw)
		next, err := base.AddNode(csd.Node{ID: "REQ-001", Type: "requirement", Title: "Deep", Fields: map[string]csd.Value{"nested": value}})
		if depth > 60 {
			code(t, err, "LIMIT")
			if next != nil || !bytes.Equal(before, serialize(t, base)) {
				t.Fatal("depth rejection was not atomic")
			}
			continue
		}
		next = must(t, next, err)
		_, err = csd.Load(serialize(t, next))
		if err != nil {
			t.Fatalf("accepted document cannot reload: %v", err)
		}
	}
}
