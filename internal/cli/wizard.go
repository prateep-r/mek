package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
	"github.com/prateep-r/mek/internal/wizard"
)

// runForm shows a form and returns its answers (test seam: wizard.Play
// drives it without a terminal).
var runForm = (*wizard.Form).Run

// providerFlow is one cloud's part of the context wizard — a GoF Strategy:
// the questions about who you are on that cloud, and how the answers become
// a context and come back from one. A new cloud is one more flow.
type providerFlow interface {
	option() wizard.Option
	steps(f *wizard.Form, w *ctxWizard) // each asked only for its cloud
	prefill(c *config.Context, a wizard.Answers)
	apply(w *ctxWizard, a wizard.Answers, c *config.Context)
}

var flows = []providerFlow{awsFlow{}, gcpFlow{}}

func flowFor(cloud string) providerFlow {
	for _, fl := range flows {
		if fl.option().Value == cloud {
			return fl
		}
	}
	return nil
}

// cloudIs is a step condition: the cloud chosen is cloud.
func cloudIs(cloud string) func(wizard.Answers) bool {
	return func(a wizard.Answers) bool { return a["cloud"] == cloud }
}

// ctxWizard is what the context wizard knows while it asks.
type ctxWizard struct {
	app    *app // runs the lookups and logins
	cfg    *config.Config
	files  []string                              // where a context can go, top layer first
	layers map[string]map[string]*config.Context // each file's own contexts
	name   string                                // adding: the name given on the command line
	old    *config.Context                       // editing: the context as its file has it
}

func newCtxWizard(app *app) (*ctxWizard, error) {
	cfg, err := provider.Load()
	if errors.Is(err, config.ErrNoConfig) { // the first context: the wizard creates the file
		cfg, err = &config.Config{Contexts: map[string]*config.Context{}}, nil
		_, cfg.Project = config.Sources()
	}
	if err != nil {
		return nil, err
	}
	w := &ctxWizard{app: app, cfg: cfg, layers: map[string]map[string]*config.Context{}}
	os.RemoveAll(provider.GCloudWizardDir()) // a login a canceled wizard left: whose, nobody knows
	if cfg.Project != "" {
		w.files = []string{filepath.Join(cfg.Project, config.LocalFile), filepath.Join(cfg.Project, config.TeamFile)}
	}
	w.files = append(w.files, config.Path())
	for _, f := range w.files {
		w.layers[f] = ownContexts(f)
	}
	return w, nil
}

// ownContexts are a file's contexts as written there: no other layer's
// protected or readonly mixed in.
func ownContexts(path string) map[string]*config.Context {
	c, err := config.File{Path: path, InProject: path != config.Path()}.Read()
	if err != nil { // Load read it a moment ago
		return nil
	}
	return c.Contexts
}

// needTerminal refuses to start a form where nobody can answer it.
func needTerminal(cmd string) error {
	if !isTerminal() {
		return fmt.Errorf("mek %s asks questions and needs a terminal — in a script, edit the file instead (see: mek help config)", cmd)
	}
	return nil
}

// choices are the files the context may be saved in. Editing a team's
// context offers your own copy in mek.local.yaml too.
func (w *ctxWizard) choices() []string {
	if w.old == nil {
		return w.files
	}
	if filepath.Base(w.old.Source) == config.TeamFile {
		return []string{w.old.Source, filepath.Join(filepath.Dir(w.old.Source), config.LocalFile)}
	}
	return []string{w.old.Source}
}

// free reports whether name can be saved in file: it isn't there, or it is
// the context being edited.
func (w *ctxWizard) free(file, name string) bool {
	if w.old != nil && file == w.old.Source && name == w.old.Name {
		return true
	}
	_, taken := w.layers[file][name]
	return !taken
}

func (w *ctxWizard) fileOptions(a wizard.Answers) []wizard.Option {
	var out []wizard.Option
	for _, f := range w.choices() {
		if w.free(f, a["name"]) {
			out = append(out, wizard.Option{Value: f, Label: config.Show(f), Hint: w.fileHint(f, a["name"])})
		}
	}
	return out
}

func (w *ctxWizard) fileHint(file, name string) string {
	if w.old != nil {
		if file != w.old.Source {
			return "your own copy — the team's stays as it is"
		}
		if filepath.Base(file) == config.TeamFile {
			return "the team's — commit the change"
		}
		return "where it is"
	}
	hint := "yours, on every project"
	switch filepath.Base(file) {
	case config.LocalFile:
		hint = "yours, this project only (not committed)"
	case config.TeamFile:
		hint = "the team's — commit it"
	}
	if lower := w.lower(file, name); lower != "" {
		hint += " · replaces the one in " + config.Show(lower)
	}
	return hint
}

// lower is the first file under file that has a context called name.
func (w *ctxWizard) lower(file, name string) string {
	for _, f := range w.files[slices.Index(w.files, file)+1:] {
		if _, ok := w.layers[f][name]; ok {
			return f
		}
	}
	return ""
}

// target is the file chosen, or the only one there is.
func (w *ctxWizard) target(a wizard.Answers) string {
	opts := w.fileOptions(a)
	for _, o := range opts {
		if o.Value == a["file"] {
			return o.Value
		}
	}
	return opts[0].Value // checkName leaves at least one
}

func (w *ctxWizard) checkName(v string, _ wizard.Answers) error {
	if err := (&config.Context{Name: v}).Validate(); err != nil {
		return err
	}
	if len(w.fileOptions(wizard.Answers{"name": v})) == 0 {
		return fmt.Errorf("%s is already in %s — change it with: mek ctx edit %s", v, config.Show(w.cfg.Contexts[v].Source), v)
	}
	return nil
}

var guardOptions = []wizard.Option{
	{Value: "", Label: "normal", Hint: "commands run as typed"},
	{Value: "protected", Label: "protected", Hint: "changes ask first; deletes need the context's name typed"},
	{Value: "readonly", Label: "readonly", Hint: "changes and deletes are blocked"},
}

func guardOf(c *config.Context) string {
	switch {
	case c.ReadOnly:
		return "readonly"
	case c.Protected:
		return "protected"
	}
	return ""
}

func (w *ctxWizard) form(title string) *wizard.Form {
	f := wizard.New(title).Prefill(w.answers())
	f.Input("name", "Name", nil, w.checkName).Help("letters, digits, '.', '_' and '-' — what you type after mek use and -c")
	f.Select("file", "Save it in", w.fileOptions, false).When(func(a wizard.Answers) bool { return len(w.fileOptions(a)) > 1 })
	var clouds []wizard.Option
	for _, fl := range flows {
		clouds = append(clouds, fl.option())
	}
	f.Select("cloud", "Cloud", wizard.Options(clouds...), false)
	for _, fl := range flows {
		fl.steps(f, w)
	}
	f.Select("guard", "Guard", wizard.Options(guardOptions...), false).Help("what mek does before a command that changes something")
	return f.Review("Save?", w.preview)
}

// answers are the form's starting values: today's, when editing.
func (w *ctxWizard) answers() wizard.Answers {
	if w.old == nil {
		return wizard.Answers{"name": w.name}
	}
	c := w.old
	a := wizard.Answers{"name": c.Name, "file": c.Source, "cloud": c.Provider, "guard": guardOf(c)}
	flowFor(c.Provider).prefill(c, a)
	return a
}

// build is the context the answers make. Editing keeps what the wizard
// doesn't ask about: clusters, targets, tunnels.
func (w *ctxWizard) build(a wizard.Answers) *config.Context {
	c := &config.Context{Provider: a["cloud"], Protected: a["guard"] == "protected", ReadOnly: a["guard"] == "readonly"}
	if w.old != nil {
		c.Clusters, c.Targets, c.Tunnels = w.old.Clusters, w.old.Targets, w.old.Tunnels
		c.UseMetadata = w.old.UseMetadata && c.Provider == config.ProviderGCP
	}
	flowFor(c.Provider).apply(w, a, c)
	return c
}

// edited is the target file with the answers in it, not yet saved.
func (w *ctxWizard) edited(a wizard.Answers) (*config.Editor, error) {
	path := w.target(a)
	e, err := config.Open(path, config.Template(path), filePerm(path))
	if err != nil {
		return nil, err
	}
	if w.old != nil && path == w.old.Source && a["name"] != w.old.Name { // renamed: in place, its comments kept
		return e, e.Rename(w.old.Name, a["name"], w.build(a))
	}
	return e, e.Set(a["name"], w.build(a))
}

// preview is what Review shows: the context as it will be written, and
// where; an error when the config would be invalid with it.
func (w *ctxWizard) preview(a wizard.Answers) (string, error) {
	e, err := w.edited(a)
	if err != nil {
		return "", err
	}
	where := "in " + config.Show(e.Path)
	if e.IsNew() {
		where += " (a new file)"
	}
	return where + ":\n" + config.Snippet(a["name"], w.build(a)), checkEdit(e)
}

// run asks, saves and says what changed.
func (w *ctxWizard) run(title string) error {
	a, err := runForm(w.form(title))
	if err != nil {
		return err
	}
	e, err := w.edited(a)
	if err == nil {
		err = saveEdit(e)
	}
	if err != nil {
		return err
	}
	w.keepLogin(a, e.Path)
	return w.report(a, e.Path)
}

// draftCtx is the context the answers make, as if saved already.
func (w *ctxWizard) draftCtx(a wizard.Answers) *config.Context {
	c := w.build(a)
	c.Name, c.Source = a["name"], w.target(a)
	c.InProject = c.Source != config.Path()
	return c
}

// draftConfig is the config with the draft in it.
func (w *ctxWizard) draftConfig(c *config.Context) *config.Config {
	cfg := &config.Config{Contexts: maps.Clone(w.cfg.Contexts), Files: w.cfg.Files, Project: w.cfg.Project}
	cfg.Contexts[c.Name] = c
	return cfg
}

func toOptions(found []provider.Found) []wizard.Option {
	out := make([]wizard.Option, len(found))
	for i, f := range found {
		out[i] = wizard.Option{Value: f.Value, Label: f.Label, Hint: f.Hint}
	}
	return out
}

func (w *ctxWizard) report(a wizard.Answers, path string) error {
	name, ok := a["name"], ui.Green("✓")
	switch {
	case w.old == nil:
		ui.Info("%s added %s to %s", ok, name, config.Show(path))
		if lower := w.lower(path, name); lower != "" {
			ui.Info("  in this project it replaces %s from %s", name, config.Show(lower))
		}
		ui.Info("  next: mek login %s (or mek use %s)", name, name)
	case path != w.old.Source:
		ui.Info("%s copied %s to %s — yours is used in this project; the team's stays as it is", ok, name, config.Show(path))
	case name != w.old.Name:
		ui.Info("%s renamed %s to %s in %s", ok, w.old.Name, name, config.Show(path))
		if config.Current() == w.old.Name {
			if err := config.SetCurrent(name); err != nil {
				return err
			}
			ui.Info("  the current context is now %s", name)
		}
		if os.Getenv("MEK_CONTEXT") == w.old.Name {
			ui.Info("  this shell's MEK_CONTEXT is still %s: mek use -s %s", w.old.Name, name)
		}
	default:
		ui.Info("%s saved %s in %s", ok, name, config.Show(path))
	}
	return nil
}

const wizardKeys = `Keys: ↑/↓ choose · type to filter a long list · enter OK · esc back (answers
are kept) · ctrl-c cancel. Nothing is written until you choose Save, and
nothing is saved that would make the config invalid.`

func (a *app) newCtxAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name]",
		Short: "Add a context, a question at a time",
		Long: `Add a context by answering questions instead of editing YAML.

It asks, in order:
  Name         what you type after mek use and -c (given here, it is filled in)
  Save it in   inside a project only: mek.local.yaml (yours, not committed),
               mek.yaml (the team's — commit it) or ~/.mek/config.yaml (yours,
               on every project). A file that has the name already isn't offered.
  Cloud        AWS or GCP
  AWS          IAM Identity Center: the access portal (portals your other
               contexts use are listed) and its region; then, logged in to the
               portal (it offers to log in first), the accounts you can reach
               and your roles in the one you pick are listed.
               Or a profile from ~/.aws/config (listed).
               Then the region for commands.
  GCP          the Google account (accounts your other contexts use are
               listed); logged in (it offers to), your projects are listed;
               then a region
  Guard        normal, protected (changes ask; deletes need the name typed)
               or readonly (changes are blocked)
  Save?        the YAML it will write, and where: Save, Back or Cancel

` + wizardKeys + `

Lists come from the cloud through mek like any command — read-only, in the
audit log. When one can't be had (not logged in, no permission, no CLI) the
step says why and you type the value. The portal's token is read from the
AWS CLI's cache and handed over in a file only you can read, removed at
once; it is never in argv or kept by mek. See: mek help wizard

The rest of the file — comments, order, other contexts — stays as it was.
A new mek.local.yaml is added to the repository's .gitignore.

  mek ctx add                  # every question
  mek ctx add orders-dev       # the name filled in
  mek context add              # "context" works wherever "ctx" does
  cd ~/src/orders && mek ctx add    # in a project: pick the file

See also: mek ctx edit, mek ctx rm, mek help config`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := needTerminal("ctx add"); err != nil {
				return err
			}
			w, err := newCtxWizard(a)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				w.name = args[0]
			}
			return w.run("Add a context")
		},
	}
}

func (a *app) newCtxEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit [name]",
		Short: "Change a context, a question at a time (no name: pick one)",
		Long: `Change a context with the same questions as mek ctx add, today's answers
filled in: press enter to keep one. Without a name it lists the contexts to
pick from.

It changes the context in the file it comes from. For one from a project's
mek.yaml it asks first:
  ./mek.yaml         the team's — commit the change
  ./mek.local.yaml   your own copy — the team's stays as it is (say, the same
                     account with your own role)
Renaming changes the name in place (and the current context, if it was it).
Clusters, targets and tunnels are kept as they are.

` + wizardKeys + `

  mek ctx edit                 # pick a context
  mek ctx edit prod            # e.g. turn on protected
  mek ctx edit orders-dev      # a team context: edit it, or copy it to mek.local.yaml

See also: mek ctx add, mek ctx rm, mek help config`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeContexts,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := needTerminal("ctx edit"); err != nil {
				return err
			}
			w, err := newCtxWizard(a)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else if name, err = w.pick(); err != nil {
				return err
			}
			ctx, err := w.cfg.Get(name)
			if err != nil {
				return err
			}
			w.old = w.layers[ctx.Source][name]
			return w.run("Edit " + name)
		},
	}
}

// pick asks which context to edit.
func (w *ctxWizard) pick() (string, error) { return pickContext(w.cfg, "Edit a context") }

// usedBy lists, for a hint, the contexts that have a value: "used by a, b".
func (w *ctxWizard) usedBy(has func(*config.Context) bool) string {
	var names []string
	for _, n := range w.cfg.Names() {
		if has(w.cfg.Contexts[n]) {
			names = append(names, n)
		}
	}
	return "used by " + strings.Join(names, ", ")
}

// regionOptions are known regions (name, place) for a Select, after "none"
// when a region is optional.
func regionOptions(regions [][2]string, none string) []wizard.Option {
	var out []wizard.Option
	if none != "" {
		out = append(out, wizard.Option{Value: "", Label: "none", Hint: none})
	}
	for _, r := range regions {
		out = append(out, wizard.Option{Value: r[0], Hint: r[1]})
	}
	return out
}

// required is a Check for an answer that can't be empty.
func required(what string) func(string, wizard.Answers) error {
	return func(v string, _ wizard.Answers) error {
		if v == "" {
			return fmt.Errorf("%s, please", what)
		}
		return nil
	}
}
