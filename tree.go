package main

import (
	"sort"
	"strings"

	"github.com/google/pprof/profile"
)

// node is one entry in the aggregated call tree we render. We collapse
// every sample stack into a tree keyed by the fully-qualified function
// names at each depth.
type node struct {
	name     string
	value    int64
	depth    int
	children map[string]*node
	parent   *node
}

func newRoot() *node {
	return &node{name: "(root)", children: map[string]*node{}}
}

func (n *node) child(name string) *node {
	if c, ok := n.children[name]; ok {
		return c
	}
	c := &node{
		name:     name,
		depth:    n.depth + 1,
		children: map[string]*node{},
		parent:   n,
	}
	n.children[name] = c
	return c
}

func (n *node) sortedChildren() []*node {
	out := make([]*node, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].value != out[j].value {
			return out[i].value > out[j].value
		}
		return out[i].name < out[j].name
	})
	return out
}

// buildTree collapses every sample in the pprof profile into a call
// tree. We pick the sample type to aggregate by index: 0 by default
// (which is `samples` for cpu profiles, `objects` for heap, etc.) or
// whatever the caller specified.
func buildTree(prof *profile.Profile, sampleIdx int, focus, ignore string) (*node, string, error) {
	if sampleIdx >= len(prof.SampleType) {
		sampleIdx = 0
	}
	st := prof.SampleType[sampleIdx]
	unit := st.Unit

	root := newRoot()
	for _, s := range prof.Sample {
		v := s.Value[sampleIdx]
		if v == 0 {
			continue
		}
		// pprof stores stack top-of-call-stack first; reverse so the
		// root of the tree is the entry function ("main") and the leaf
		// is whatever was actually running.
		frames := make([]string, 0, len(s.Location))
		for i := len(s.Location) - 1; i >= 0; i-- {
			loc := s.Location[i]
			for j := len(loc.Line) - 1; j >= 0; j-- {
				line := loc.Line[j]
				if line.Function == nil {
					continue
				}
				name := line.Function.Name
				if name == "" {
					name = line.Function.SystemName
				}
				frames = append(frames, name)
			}
		}
		if focus != "" && !stackContains(frames, focus) {
			continue
		}
		if ignore != "" && stackContains(frames, ignore) {
			continue
		}
		cursor := root
		root.value += v
		for _, name := range frames {
			cursor = cursor.child(name)
			cursor.value += v
		}
	}
	return root, unit, nil
}

func stackContains(frames []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, f := range frames {
		if strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}
