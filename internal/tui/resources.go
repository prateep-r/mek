package tui

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/prateep-r/mek/internal/catalog"
	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
	"github.com/prateep-r/mek/internal/provider"
)

// Levels of the Resources tab — each a step down from the one before.
const (
	levelServices = iota
	levelKinds
	levelItems
	levelDetail
)

// resourcesTab browses the chosen context's cloud from its catalog:
// services, their kinds of resource, the resources, one resource. Lists
// and details are mek runs of the cloud's CLI, in the background and
// quiet; actions are mek runs too, so the guard and audit log apply.
type resourcesTab struct {
	ctx   string // the context it shows; another one starts it over
	cloud string
	cat   *catalog.Catalog
	src   catalog.Source
	err   error

	level   int
	lists   [4]list
	filter  string // over the names at this level
	svc     *catalog.Service
	res     *catalog.Resource
	rows    []catalog.Row
	next    string // the next page of rows ("" when none)
	moreErr error  // why the next page didn't come (the rows so far stay)
	row     *catalog.Row
	detail  []string // the resource's JSON, line by line
	loading string   // what is being fetched ("" when nothing)
	gen     int      // which fetch counts: older ones are dropped
}

// listedMsg and describedMsg bring a fetch's result.
type listedMsg struct {
	gen  int
	rows []catalog.Row
	next string
	err  error
}

type describedMsg struct {
	gen  int
	text []byte
	err  error
}

// fetch runs mek for a lookup and keeps its output (test seam): quiet, with
// no terminal, so neither the CLI nor mek can ask anything.
var fetch = func(mek string, args []string) ([]byte, error) {
	c := exec.Command(mek, args...)
	c.Env = append(os.Environ(), "MEK_SHOW_CMD=0", "CLOUDSDK_CORE_DISABLE_PROMPTS=1", "AWS_PAGER=")
	out, err := c.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil, errors.New(reason(string(exit.Stderr), err))
	}
	return out, err
}

var loadCatalog = catalog.Load // test seam

var errorLine = regexp.MustCompile(`(?i)\berror\b`)

// reason is why a run failed, from what it printed: its first line that
// says error (gcloud explains below it), else its last.
func reason(stderr string, err error) string {
	for _, l := range strings.Split(stderr, "\n") {
		if errorLine.MatchString(l) {
			return strings.TrimSpace(l)
		}
	}
	return cmp.Or(lastLine(stderr), err.Error())
}

func (*resourcesTab) title() string { return "Resources" }

func (t *resourcesTab) help() string {
	switch t.level {
	case levelServices, levelKinds:
		return "enter open · / filter · esc back"
	case levelItems:
		return "enter details · a actions · / filter · r reload · esc back"
	}
	return "↑↓ scroll · a actions · esc back"
}

// sync starts over when another context is chosen.
func (t *resourcesTab) sync(m *model) {
	c := m.context(m.selected)
	if c == nil || c.Name == t.ctx {
		return
	}
	*t = resourcesTab{ctx: c.Name, cloud: c.Cloud, gen: t.gen + 1}
	t.cat, t.err = loadCatalog(c.Cloud)
	if t.err == nil {
		t.src, t.err = catalog.For(c.Cloud)
	}
}

// names are what this level lists, filtered, with the index of each in its
// unfiltered list; an exact match comes first.
func (t *resourcesTab) names() (names []string, index []int) {
	f := strings.ToLower(t.filter)
	exact := 0 // how many exact matches lead
	add := func(i int, name string, keys ...string) {
		switch {
		case f != "" && slices.ContainsFunc(append(keys, name), func(k string) bool { return strings.ToLower(k) == f }):
			names, index = slices.Insert(names, exact, name), slices.Insert(index, exact, i)
			exact++
		case strings.Contains(strings.ToLower(name), f):
			names, index = append(names, name), append(index, i)
		}
	}
	switch t.level {
	case levelServices:
		for i, s := range t.cat.Services {
			add(i, s.Name)
		}
	case levelKinds:
		for i, r := range t.svc.Resources {
			add(i, r.Name)
		}
	case levelItems:
		for i, r := range t.rows {
			add(i, r.ID+" "+r.Name+" "+r.State+" "+r.Where, r.ID, r.Name)
		}
	}
	return names, index
}

func (t *resourcesTab) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	t.sync(m)
	if t.src == nil { // no contexts, or no catalog for its cloud
		return nil
	}
	if t.level == levelDetail {
		return t.detailKey(m, k)
	}
	_, index := t.names()
	l := &t.lists[t.level]
	if l.key(k.String(), len(index)) {
		if t.level == levelItems && l.cursor == len(index)-1 && t.next != "" && t.loading == "" {
			return t.list(m, t.next) // the last row: fetch the next page
		}
		return nil
	}
	switch k.String() {
	case "/":
		m.overlay = &filterScreen{t}
	case "esc", "backspace", "left":
		t.up()
	case "a":
		if t.level == levelItems && len(index) > 0 {
			t.row = &t.rows[index[l.cursor]]
			return t.actions(m)
		}
	case "enter", "right":
		if len(index) == 0 {
			return nil
		}
		return t.open(m, index[l.cursor])
	}
	return nil
}

// open goes down into the i-th entry of this level.
func (t *resourcesTab) open(m *model, i int) tea.Cmd {
	t.filter = ""
	switch t.level {
	case levelServices:
		t.svc, t.level, t.lists[levelKinds] = &t.cat.Services[i], levelKinds, list{}
	case levelKinds:
		t.res, t.level, t.lists[levelItems] = &t.svc.Resources[i], levelItems, list{}
		return t.list(m, "")
	case levelItems:
		t.row, t.level, t.lists[levelDetail] = &t.rows[i], levelDetail, list{}
		return t.describe(m)
	}
	return nil
}

// up goes back a level, dropping any fetch on the way.
func (t *resourcesTab) up() {
	t.filter, t.loading, t.err = "", "", nil
	t.gen++
	if t.level > levelServices {
		t.level--
	}
}

// refresh lists the resources again (r) — after a failed list too.
func (t *resourcesTab) refresh(m *model) tea.Cmd {
	if t.level == levelItems {
		return t.list(m, "")
	}
	return nil
}

// list fetches the first page of rows, or the next.
func (t *resourcesTab) list(m *model, next string) tea.Cmd {
	t.gen++
	gen, src, svc, res := t.gen, t.src, t.svc, t.res
	t.loading, t.err, t.moreErr = "listing "+svc.Name+" "+res.Name, nil, nil
	if next == "" {
		t.rows, t.next = nil, ""
	} else {
		t.loading += " (more)"
	}
	args := append([]string{"-c", t.ctx}, src.List(svc, res, next)...)
	return func() tea.Msg {
		out, err := fetch(m.mek, args)
		if err != nil {
			return listedMsg{gen: gen, err: err}
		}
		rows, next, err := src.Rows(res, out)
		return listedMsg{gen: gen, rows: rows, next: next, err: err}
	}
}

// describe fetches the chosen resource's details, or shows its row's.
func (t *resourcesTab) describe(m *model) tea.Cmd {
	argv := t.src.Describe(t.svc, t.res, *t.row)
	if argv == nil {
		t.detail = pretty(t.row.Raw)
		return nil
	}
	t.gen++
	gen := t.gen
	t.loading, t.detail = "describing "+t.row.ID, nil
	args := append([]string{"-c", t.ctx}, argv...)
	return func() tea.Msg {
		out, err := fetch(m.mek, args)
		return describedMsg{gen: gen, text: out, err: err}
	}
}

// receive takes a fetch's result, unless it was dropped.
func (t *resourcesTab) receive(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case listedMsg:
		switch {
		case msg.gen != t.gen:
		case msg.err != nil && len(t.rows) > 0: // a later page: the rows so far stay
			t.loading, t.moreErr = "", msg.err
		default:
			t.loading = ""
			t.rows, t.next, t.err = append(t.rows, msg.rows...), msg.next, msg.err
		}
	case describedMsg:
		if msg.gen == t.gen {
			t.loading = ""
			t.detail = pretty(msg.text)
			if msg.err != nil {
				t.detail = []string{red.Render("✗ " + msg.err.Error())}
			}
		}
	default:
		return false
	}
	return true
}

// pretty is JSON indented, line by line.
func pretty(b []byte) []string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return strings.Split(out.String(), "\n")
}

func (t *resourcesTab) detailKey(m *model, k tea.KeyPressMsg) tea.Cmd {
	l := &t.lists[levelDetail]
	switch k.String() {
	case "esc", "backspace", "left":
		t.up()
	case "a":
		return t.actions(m)
	case "up", "k":
		l.offset--
	case "down", "j":
		l.offset++
	case "pgup":
		l.offset -= 10
	case "pgdown":
		l.offset += 10
	case "home", "g":
		l.offset = 0
	case "end", "G":
		l.offset = len(t.detail)
	}
	l.offset = max(0, min(l.offset, len(t.detail)-1))
	return nil
}

// actions opens the chosen resource's actions.
func (t *resourcesTab) actions(m *model) tea.Cmd {
	if len(t.res.Actions) == 0 {
		m.status = dim.Render(t.svc.Name + " " + t.res.Name + " has no actions that need only the resource's id")
		return nil
	}
	m.overlay = &actionsScreen{t: t}
	return nil
}

// act is an action on the chosen resource, asked first: with the context's
// name typed for a destructive command on a protected context (as mek
// would), else yes or no — mek ui asks even where mek wouldn't, as one key
// is easier to press by mistake than a typed command. On a readonly context
// mek blocks it, and the audit log has the attempt.
func (t *resourcesTab) act(m *model, a catalog.Action) tea.Cmd {
	c := m.context(t.ctx)
	if c == nil { // removed while the menu was open
		return nil
	}
	argv := t.src.Act(t.svc, t.res, a, *t.row)
	what := fmt.Sprintf("%s %s (%s)", a.Command, t.row.ID, t.ctx)
	act := action{label: what, args: append([]string{"-c", t.ctx}, argv...), after: t.refresh, data: true}
	cloud, _ := provider.Lookup(t.cloud) // it has a catalog: a cloud mek knows
	class := cloud.Classify(argv[1:])
	switch guard.Decide(&config.Context{Protected: c.Protected, ReadOnly: c.ReadOnly}, class) {
	case guard.Block:
	case guard.ConfirmTyped:
		act.confirm = fmt.Sprintf("%s is protected — %s: %s", t.ctx, class, strings.Join(argv, " "))
		act.typed = t.ctx
	default:
		act.confirm = fmt.Sprintf("%s: %s?", class, strings.Join(argv, " "))
	}
	return m.start(act)
}

func (t *resourcesTab) view(m *model, w, h int) string {
	t.sync(m)
	c := m.context(m.selected)
	if c == nil {
		return dim.Render("  no contexts")
	}
	head := "  " + bold.Render(c.Name)
	if tg := tags(c); tg != "" {
		head += " " + tg
	}
	if t.src == nil {
		return head + "\n\n" + red.Render("  ✗ "+t.err.Error())
	}
	path := []string{t.cloud}
	if t.level > levelServices {
		path = append(path, t.svc.Name)
	}
	if t.level > levelKinds {
		path = append(path, t.res.Name)
	}
	if t.level > levelItems {
		path = append(path, t.row.ID)
	}
	head += "  " + strings.Join(path, " › ")
	switch {
	case t.loading != "":
		head += dim.Render("  " + t.loading + "…")
	case t.filter != "":
		head += yellow.Render(fmt.Sprintf("  %q", t.filter))
	case t.level == levelServices:
		head += dim.Render("  (" + t.cat.Source + ")")
	}
	body := t.body(w, h-2)
	if t.err != nil {
		body = red.Render("  ✗ " + t.err.Error())
	}
	return head + "\n\n" + body
}

func (t *resourcesTab) body(w, h int) string {
	l := &t.lists[t.level]
	names, index := t.names()
	switch t.level {
	case levelServices:
		var rows [][]string
		for i, n := range names {
			rows = append(rows, []string{bold.Render(n), fmt.Sprint(len(t.cat.Services[index[i]].Resources))})
		}
		return l.render([]string{"SERVICE", "KINDS"}, rows, w, h)
	case levelKinds:
		var rows [][]string
		for i, n := range names {
			r := t.svc.Resources[index[i]]
			var verbs []string
			for _, a := range r.Actions {
				f := strings.Fields(a.Command)
				verbs = append(verbs, f[len(f)-1])
			}
			rows = append(rows, []string{bold.Render(n), strings.Join(verbs, " ")})
		}
		return l.render([]string{"KIND", "ACTIONS"}, rows, w, h)
	case levelItems:
		var rows [][]string
		for _, i := range index {
			r := t.rows[i]
			state := r.State
			switch strings.ToLower(state) {
			case "running", "available", "active", "runnable", "ready":
				state = green.Render(state)
			case "stopped", "terminated", "failed", "deleting", "error":
				state = yellow.Render(state)
			}
			row := []string{r.ID, r.Name, state}
			if t.cloud != "aws" {
				row = append(row, r.Where)
			}
			rows = append(rows, row)
		}
		header := []string{"ID", "NAME", "STATE"}
		if t.cloud != "aws" {
			header = append(header, "WHERE")
		}
		more := ""
		switch {
		case t.moreErr != nil:
			more = "\n" + red.Render("  ✗ the next page: "+t.moreErr.Error()) + dim.Render(" — ↓ at the last row tries again")
		case t.next != "":
			more = "\n" + dim.Render("  more below — ↓ at the last row fetches them")
		case t.cloud == "gcp" && len(t.rows) >= catalog.GCPLimit:
			more = "\n" + dim.Render(fmt.Sprintf("  the first %d only — for the rest, mek -c %s gcloud … list --filter", catalog.GCPLimit, t.ctx))
		}
		if t.loading != "" && len(rows) == 0 {
			return ""
		}
		return l.render(header, rows, w, h-len(strings.Split(more, "\n"))+1) + more
	}
	fit := max(1, h)
	var out []string
	for _, line := range t.detail[min(l.offset, len(t.detail)):min(len(t.detail), l.offset+fit)] {
		out = append(out, ansi.Truncate("  "+line, w, "…"))
	}
	return strings.Join(out, "\n")
}

// filterScreen edits the Resources tab's filter while showing its list.
type filterScreen struct{ t *resourcesTab }

func (*filterScreen) help() string { return "type to filter · enter keep · esc clear" }

func (f *filterScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		m.overlay = nil
	case "esc":
		f.t.filter, m.overlay = "", nil
	case "backspace":
		if r := []rune(f.t.filter); len(r) > 0 {
			f.t.filter = string(r[:len(r)-1])
		}
	default:
		f.t.filter += k.Text
	}
	f.t.lists[f.t.level].cursor = 0
	return nil
}

func (f *filterScreen) view(m *model, w, h int) string {
	return f.t.view(m, w, h-1) + "\n" + bold.Render("/ ") + f.t.filter + "▏"
}

// actionsScreen picks an action for the chosen resource.
type actionsScreen struct {
	t *resourcesTab
	l list
}

func (*actionsScreen) help() string { return "↑↓ choose · enter run · esc back" }

func (s *actionsScreen) key(m *model, k tea.KeyPressMsg) tea.Cmd {
	acts := s.t.res.Actions
	if s.l.key(k.String(), len(acts)) {
		return nil
	}
	switch k.String() {
	case "esc", "q":
		m.overlay = nil
	case "enter":
		m.overlay = nil
		return s.t.act(m, acts[s.l.cursor])
	}
	return nil
}

func (s *actionsScreen) view(m *model, w, h int) string {
	var rows [][]string
	for _, a := range s.t.res.Actions {
		rows = append(rows, []string{bold.Render(a.Command), dim.Render(strings.Join(s.t.src.Act(s.t.svc, s.t.res, a, *s.t.row), " "))})
	}
	head := "  " + bold.Render("actions on "+s.t.row.ID) + dim.Render(" ("+s.t.ctx+") — run through mek: guard and audit log apply")
	return head + "\n\n" + s.l.render([]string{"ACTION", "RUNS"}, rows, w, h-2)
}
