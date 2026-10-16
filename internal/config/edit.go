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
			_, after, _ := strings.Cut(l.lines[l.keyLine], ":")
			if v, _, _ := strings.Cut(after, "#"); strings.TrimSpace(v) != "" {
				l.flow = true // contexts: null (or ~): rewritten whole too
				return l, nil
			}
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

// SnippetOf is any value as YAML, the way Set writes it (for a preview).
func SnippetOf(v any) string {
	b, _ := yaml.MarshalWithOptions(v, yaml.Indent(2)) // config types always marshal
	return strings.TrimRight(string(b), "\n")
}

// Snippet is a context as Set writes it, unindented (for a preview).
func Snippet(name string, c *Context) string { return strings.Join(render(name, c, 0), "\n") }

// render writes one context as YAML lines, indented.
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
	at, lines := l.endLine, render(name, c, l.indent) // after the last entry…
	if len(l.entries) == 0 {
		at = l.tailEnd // …or, with none yet, after contexts: and the comments under it (a template's examples)
		if at > l.keyLine {
			lines = append([]string{""}, lines...) // apart from them: removing it later leaves them be
		}
	}
	e.splice(l.lines, at+1, at, lines)
	return nil
}

// Rename replaces context old with c called name, where old was: the
// comments above it stay with it.
func (e *Editor) Rename(old, name string, c *Context) error {
	l, err := e.layout()
	if err != nil {
		return err
	}
	if l.flow {
		return e.rewrite(l, func(cs map[string]*Context) error {
			delete(cs, old)
			cs[name] = c
			return nil
		})
	}
	for _, en := range l.entries {
		if en.name == old {
			e.splice(l.lines, en.first, en.last, render(name, c, l.indent))
			return nil
		}
	}
	return fmt.Errorf("%s has no context %q", Show(e.Path), old)
}

// pairEnd is the last line of pairs[i]: up to the next pair, or to limit for
// the last one, without the blank and comment lines before what follows.
func pairEnd(lines []string, pairs []*ast.MappingValueNode, i, limit int) int {
	if i+1 < len(pairs) {
		limit = line(pairs[i+1].Key) - 1
	}
	return trimBack(lines, line(pairs[i].Key), limit)
}

// renderAt writes {key: v} as YAML lines, indented.
func renderAt(key string, v any, indent int) []string {
	out := strings.Split(SnippetOf(map[string]any{key: v}), "\n")
	for i := range out {
		out[i] = strings.Repeat(" ", indent) + out[i]
	}
	return out
}

// section finds the block mapping of a context's section (tunnels,
// targets, clusters). ok is false when something on the way is in flow
// style: the caller then rewrites the whole context (Set).
type section struct {
	lines              []string
	ctxPairs           []*ast.MappingValueNode // the context's keys
	ctxIndent, ctxLast int                     // their indentation; the context's last line
	at                 int                     // the section among ctxPairs (-1: none)
	pairs              []*ast.MappingValueNode // its entries
	last               int                     // its last line
}

func (e *Editor) section(ctx, name string) (*section, bool, error) {
	l, err := e.layout()
	if err != nil || l.flow {
		return nil, false, err
	}
	f, _ := parser.ParseBytes(e.src, parser.ParseComments) // layout parsed it
	var cv ast.Node
	for _, p := range pairs(f.Docs[0].Body) {
		if keyName(p) == "contexts" {
			for _, c := range pairs(p.Value) {
				if keyName(c) == ctx {
					cv = c.Value
				}
			}
		}
	}
	m, ok := cv.(*ast.MappingNode)
	if !ok || m.IsFlowStyle {
		return nil, false, nil
	}
	sec := &section{lines: l.lines, ctxPairs: m.Values, ctxIndent: m.Values[0].Key.GetToken().Position.Column - 1, at: -1}
	for _, en := range l.entries {
		if en.name == ctx {
			sec.ctxLast = en.last
		}
	}
	for i, p := range m.Values {
		if keyName(p) != name {
			continue
		}
		sm, ok := p.Value.(*ast.MappingNode)
		if !ok || sm.IsFlowStyle {
			return nil, false, nil
		}
		sec.at, sec.pairs, sec.last = i, sm.Values, pairEnd(l.lines, m.Values, i, sec.ctxLast)
	}
	return sec, true, nil
}

// SetEntry sets one entry of a context's section — tunnels.db, say — in
// place, leaving the rest of the context (comments, other entries) as it
// was. ok is false when the context or section is written in flow style
// ({…}); the caller then rewrites the whole context with Set.
func (e *Editor) SetEntry(ctx, name, key string, v any) (ok bool, err error) {
	sec, ok, err := e.section(ctx, name)
	if !ok || err != nil {
		return false, err
	}
	if sec.at < 0 { // no such section yet: at the end of the context
		lines := append([]string{strings.Repeat(" ", sec.ctxIndent) + name + ":"}, renderAt(key, v, sec.ctxIndent+2)...)
		e.splice(sec.lines, sec.ctxLast+1, sec.ctxLast, lines)
		return true, nil
	}
	indent := sec.pairs[0].Key.GetToken().Position.Column - 1
	for i, p := range sec.pairs {
		if keyName(p) == key {
			e.splice(sec.lines, line(p.Key), pairEnd(sec.lines, sec.pairs, i, sec.last), renderAt(key, v, indent))
			return true, nil
		}
	}
	e.splice(sec.lines, sec.last+1, sec.last, renderAt(key, v, indent))
	return true, nil
}

// DeleteEntry removes one entry of a context's section in place (the
// section too, when it was the only one); ok as for SetEntry.
func (e *Editor) DeleteEntry(ctx, name, key string) (ok bool, err error) {
	sec, ok, err := e.section(ctx, name)
	if !ok || err != nil {
		return false, err
	}
	for i, p := range sec.pairs {
		if keyName(p) != key {
			continue
		}
		first, last := line(p.Key), pairEnd(sec.lines, sec.pairs, i, sec.last)
		if len(sec.pairs) == 1 { // the section goes with it
			first = line(sec.ctxPairs[sec.at].Key)
		}
		e.splice(sec.lines, first, last, nil)
		return true, nil
	}
	return false, fmt.Errorf("%s: context %q has no %s.%s", Show(e.Path), ctx, name, key)
}

// rewrite turns a flow-style contexts: {…} (or null) into block style,
// changed by change.
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
			// Its own comment, right above it at its indent, goes with it —
			// not one indented under the entry before.
			first, own := en.first, strings.Repeat(" ", l.indent)+"#"
			for first > l.keyLine+1 && strings.HasPrefix(l.lines[first-1], own) {
				first--
			}
			e.splice(l.lines, first, en.last, nil)
			return nil
		}
	}
	return missing
}

// SetSetting sets one key under settings: (adding the section at the end
// of the file when there is none), leaving the rest of the file as it was.
func (e *Editor) SetSetting(key string, value any) error {
	if _, err := e.layout(); err != nil { // parses, and says so when it can't
		return err
	}
	f, _ := parser.ParseBytes(e.src, parser.ParseComments)
	lines := strings.Split(string(e.src), "\n")
	root := pairs(f.Docs[0].Body)
	for i, p := range root {
		if keyName(p) != "settings" {
			continue
		}
		limit := len(lines) - 1
		if i+1 < len(root) {
			limit = line(root[i+1].Key) - 1
		}
		if m, ok := p.Value.(*ast.MappingNode); ok && !m.IsFlowStyle {
			kv := renderAt(key, value, m.Values[0].Key.GetToken().Position.Column-1)
			for j, c := range m.Values {
				if keyName(c) == key {
					e.splice(lines, line(c.Key), pairEnd(lines, m.Values, j, limit), kv)
					return nil
				}
			}
			at := pairEnd(lines, m.Values, len(m.Values)-1, limit)
			e.splice(lines, at+1, at, kv)
			return nil
		}
		// settings: {…}, or empty: rewritten as a block.
		var cur struct {
			Settings map[string]any `yaml:"settings"`
		}
		yaml.Unmarshal(e.src, &cur) // parsed above
		if cur.Settings == nil {
			cur.Settings = map[string]any{}
		}
		cur.Settings[key] = value
		e.splice(lines, line(p.Key), trimBack(lines, line(p.Key), limit), strings.Split(SnippetOf(map[string]any{"settings": cur.Settings}), "\n"))
		return nil
	}
	text := strings.TrimRight(string(e.src), "\n")
	if text != "" {
		text += "\n\n"
	}
	e.src = []byte(text + SnippetOf(map[string]any{"settings": map[string]any{key: value}}) + "\n")
	return nil
}

// Save writes the file atomically (creating its directory). A symlink —
// to a dotfiles repository, say — stays one: the file it points to is
// written.
func (e *Editor) Save() error {
	if err := os.MkdirAll(filepath.Dir(e.Path), 0o700); err != nil {
		return err
	}
	path := e.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := fsutil.WriteFileAtomic(path, e.src, e.perm); err != nil {
		return err
	}
	e.isNew = false
	return nil
}

// Preview is Load with the file at path holding b instead of what is on
// disk: what the config would be after an edit, to validate before saving.
func Preview(path string, b []byte) (*Config, error) {
	layers, project := Sources()
	found := false
	src := override(layers, path, b, &found)
	if !found { // a file mek doesn't read yet (a new mek.local.yaml): check it on top
		base := filepath.Base(path)
		src = Layers{bytesSource{File{path, base == TeamFile || base == LocalFile}, b}, src}
	}
	c, err := src.Read()
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

// override swaps the File at path for one holding b, through nested Layers,
// and sets found when there is one.
func override(s Source, path string, b []byte, found *bool) Source {
	switch s := s.(type) {
	case Layers:
		out := make(Layers, len(s))
		for i, x := range s {
			out[i] = override(x, path, b, found)
		}
		return out
	case File:
		if s.Path == path {
			*found = true
			return bytesSource{s, b}
		}
	}
	return s
}
