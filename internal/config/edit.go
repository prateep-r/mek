package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/prateep-r/mek/internal/fsutil"
)

// Editor changes contexts in one config file and leaves the rest of it —
// comments, order, spacing — byte for byte as it was: a GoF Facade over
// the YAML parsing and line splicing that takes. It reads positions from
// the parsed file and replaces only the lines of the context it changes.
type Editor struct {
	Path  string
	src   []byte
	perm  os.FileMode
	isNew bool // the file doesn't exist yet: it starts from a template
}

// Open reads a config file to edit; a missing one starts as template.
func Open(path string, template []byte, perm os.FileMode) (*Editor, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Editor{Path: path, src: bytes.Clone(template), perm: perm, isNew: true}, nil
	}
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	return &Editor{Path: path, src: b, perm: perm}, nil
}

// Bytes is the file as it would be saved.
func (e *Editor) Bytes() []byte { return e.src }

// IsNew reports whether Save creates the file.
func (e *Editor) IsNew() bool { return e.isNew }

// entry is where one context sits in the file: lines first..last
// (0-based, inclusive).
type entry struct {
	name        string
	first, last int
}

// layout is where the contexts mapping is and what is in it.
type layout struct {
	lines   []string
	hasKey  bool    // there is a contexts: key
	flow    bool    // written as {…}: rewritten whole, in block style
	keyLine int     // the contexts: line (0-based)
	endLine int     // the last line of the last entry (0-based)
	tailEnd int     // the last non-blank line of the section, comments included
	entries []entry // in file order
	indent  int     // the entries' indentation
}

func (e *Editor) layout() (*layout, error) {
	l := &layout{lines: strings.Split(string(e.src), "\n"), indent: 2}
	f, err := parser.ParseBytes(e.src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("can't edit %s: %w", Show(e.Path), err)
	}
	var root []*ast.MappingValueNode
	if len(f.Docs) > 0 {
		root = pairs(f.Docs[0].Body)
	}
	for i, p := range root {
		if keyName(p) != "contexts" {
			continue
		}
		l.hasKey, l.keyLine = true, line(p.Key)
		l.endLine = len(l.lines) - 1
		if i+1 < len(root) {
			l.endLine = line(root[i+1].Key) - 1
		}
		l.tailEnd = l.endLine
		for l.tailEnd > l.keyLine && strings.TrimSpace(l.lines[l.tailEnd]) == "" {
			l.tailEnd--
		}
		l.endLine = trimBack(l.lines, l.keyLine, l.endLine)
		switch v := p.Value.(type) {
		case *ast.MappingNode:
			if v.IsFlowStyle {
				l.flow = true
				return l, nil
			}
		case *ast.NullNode, nil:
		default:
			return nil, fmt.Errorf("can't edit %s: contexts: isn't a mapping", Show(e.Path))
		}
		ps := pairs(p.Value)
		for j, c := range ps {
			en := entry{name: keyName(c), first: line(c.Key), last: l.endLine}
			if j+1 < len(ps) {
				en.last = trimBack(l.lines, en.first, line(ps[j+1].Key)-1)
			}
			l.entries = append(l.entries, en)
			l.indent = c.Key.GetToken().Position.Column - 1
		}
	}
	return l, nil
}

// pairs is a mapping's key: value pairs (none for anything else).
func pairs(n ast.Node) []*ast.MappingValueNode {
	if m, ok := n.(*ast.MappingNode); ok {
		return m.Values
	}
	return nil
}

func keyName(p *ast.MappingValueNode) string { return p.Key.GetToken().Value }
func line(n ast.Node) int                    { return n.GetToken().Position.Line - 1 }

// trimBack moves last up past blank and comment-only lines (but not above
// first): comments before the next entry stay with it.
func trimBack(lines []string, first, last int) int {
	for last > first {
		if t := strings.TrimSpace(lines[last]); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		last--
	}
	return last
}

// render writes one context as YAML lines, indented.
// Snippet is a context as Set writes it, unindented (for a preview).
func Snippet(name string, c *Context) string { return strings.Join(render(name, c, 0), "\n") }

func render(name string, c *Context, indent int) []string {
	b, _ := yaml.MarshalWithOptions(map[string]*Context{name: c}, yaml.Indent(2)) // a Context always marshals
	out := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := range out {
		out[i] = strings.Repeat(" ", indent) + out[i]
	}
	return out
}

func (e *Editor) splice(lines []string, first, last int, with []string) {
	out := append(append(append([]string{}, lines[:first]...), with...), lines[last+1:]...)
	e.src = []byte(strings.Join(out, "\n"))
}

// Set adds a context, or replaces the one of that name.
func (e *Editor) Set(name string, c *Context) error {
	l, err := e.layout()
	if err != nil {
		return err
	}
	switch {
	case !l.hasKey:
		text := strings.TrimRight(string(e.src), "\n")
		if text != "" {
			text += "\n"
		}
		e.src = []byte(text + "contexts:\n" + strings.Join(render(name, c, 2), "\n") + "\n")
		return nil
	case l.flow:
		return e.rewrite(l, func(cs map[string]*Context) error { cs[name] = c; return nil })
	}
	for _, en := range l.entries {
		if en.name == name {
			e.splice(l.lines, en.first, en.last, render(name, c, l.indent))
			return nil
		}
	}
	at := l.endLine // after the last entry…
	if len(l.entries) == 0 {
		at = l.tailEnd // …or, with none yet, after contexts: and the comments under it (a template's examples)
	}
	e.splice(l.lines, at+1, at, render(name, c, l.indent))
	return nil
}

// rewrite turns a flow-style contexts: {…} into block style, changed by
// change.
func (e *Editor) rewrite(l *layout, change func(map[string]*Context) error) error {
	cur, err := decode(e.src, e.Path, false)
	if err != nil {
		return err
	}
	if err := change(cur.Contexts); err != nil {
		return err
	}
	head := l.lines[l.keyLine]
	block := []string{head[:strings.Index(head, "contexts")] + "contexts:"}
	if len(cur.Contexts) == 0 {
		block[0] += " {}"
	}
	for _, n := range cur.Names() {
		block = append(block, render(n, cur.Contexts[n], 2)...)
	}
	e.splice(l.lines, l.keyLine, l.endLine, block)
	return nil
}

// Delete removes a context.
func (e *Editor) Delete(name string) error {
	l, err := e.layout()
	if err != nil {
		return err
	}
	missing := fmt.Errorf("%s has no context %q", Show(e.Path), name)
	if l.flow {
		return e.rewrite(l, func(cs map[string]*Context) error {
			if cs[name] == nil {
				return missing
			}
			delete(cs, name)
			return nil
		})
	}
	for _, en := range l.entries {
		if en.name == name {
			first := en.first // its own comment, right above it, goes with it
			for first > l.keyLine+1 && strings.HasPrefix(strings.TrimSpace(l.lines[first-1]), "#") {
				first--
			}
			e.splice(l.lines, first, en.last, nil)
			return nil
		}
	}
	return missing
}

// Save writes the file atomically (creating its directory).
func (e *Editor) Save() error {
	if err := os.MkdirAll(filepath.Dir(e.Path), 0o700); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(e.Path, e.src, e.perm); err != nil {
		return err
	}
	e.isNew = false
	return nil
}

// Preview is Load with the file at path holding b instead of what is on
// disk: what the config would be after an edit, to validate before saving.
func Preview(path string, b []byte) (*Config, error) {
	src, project := Sources()
	c, err := override(src, path, b).Read()
	if err != nil {
		return nil, err
	}
	c.Project = project
	return c, c.validate()
}

// bytesSource is a File whose content is given.
type bytesSource struct {
	File
	b []byte
}

func (s bytesSource) Read() (*Config, error) {
	c, err := decode(s.b, s.Path, s.InProject)
	if err != nil {
		return nil, err
	}
	c.Files = []string{s.Path}
	return c, nil
}

// override swaps the File at path for one holding b, through nested Layers.
func override(s Source, path string, b []byte) Source {
	switch s := s.(type) {
	case Layers:
		out := make(Layers, len(s))
		for i, x := range s {
			out[i] = override(x, path, b)
		}
		return out
	case File:
		if s.Path == path {
			return bytesSource{s, b}
		}
	}
	return s
}
