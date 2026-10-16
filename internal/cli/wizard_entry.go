package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/provider"
	"github.com/prateep-r/mek/internal/ui"
	"github.com/prateep-r/mek/internal/wizard"
)

// entryKind is a section of a context the wizard edits — targets, clusters
// or tunnels — a GoF Strategy: its questions on each cloud, and how the
// answers become an entry and come back from one.
type entryKind interface {
	section() string // the YAML key: "tunnels"
	noun() string    // "tunnel"
	names(c *config.Context) []string
	describe(c *config.Context, name string) string
	steps(f *wizard.Form, e *entryWizard) // ends with e.nameStep
	prefill(c *config.Context, name string, a wizard.Answers)
	apply(e *entryWizard, a wizard.Answers, c *config.Context) // puts entry a["name"] in c
	remove(c *config.Context, name string)
	entry(c *config.Context, name string) any
	next(ctx, name string) string // what to run with it
}

// entryWizard is what the wizard knows while it asks about one entry.
type entryWizard struct {
	app    *app
	kind   entryKind
	ctx    *config.Context   // the context as its file has it
	l      *loaded           // the context, ready for lookups
	old    string            // editing: the entry's name
	name   string            // adding: the name given on the command line
	extra  map[string]string // what a lookup found with each value (zone, port, …)
	labels map[string]string // and its name, for a default
	mu     sync.Mutex        // over extra and labels: lookups may run at once
}

// openEntry loads the context to change: -c, or picked from a list (the
// commands make sure there is a terminal for it).
func (a *app) openEntry(k entryKind) (*entryWizard, error) {
	cfg, err := provider.Load()
	if err != nil {
		return nil, err
	}
	name := a.opts.context
	if name == "" {
		if name, err = pickContext(cfg, "Which context?"); err != nil {
			return nil, err
		}
	}
	ctx, err := cfg.Get(name)
	if err != nil {
		return nil, err
	}
	if ctx.Provider == config.ProviderHuawei {
		return nil, fmt.Errorf("%s is a huawei context: those have no %s", name, k.section())
	}
	e := &entryWizard{app: a, kind: k, ctx: ownContexts(ctx.Source)[name], extra: map[string]string{}, labels: map[string]string{}}
	return e, nil
}

// prepare readies the context's environment for the lookups; it is the
// wizard's own, not saved, so a lookup's stderr stays off the form.
func (e *entryWizard) prepare() error {
	l, err := e.app.load(e.ctx.Name)
	if err != nil {
		return err
	}
	l.quiet = true
	e.l = l
	return nil
}

// lookup runs a discovery command in the context and remembers what came
// with each value.
func (e *entryWizard) lookup(argv []string, parse func([]byte) ([]provider.Found, error)) ([]wizard.Option, error) {
	out, err := e.app.query(e.l)(argv)
	if err != nil {
		return nil, err
	}
	found, err := parse(out)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range found {
		e.extra[f.Value], e.labels[f.Value] = f.Extra, f.Label
	}
	return toOptions(found), err
}

// extraOf and labelOf are what a lookup found with value v (under mu: a
// lookup may still be writing).
func (e *entryWizard) extraOf(v string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.extra[v]
}

func (e *entryWizard) labelOf(v string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.labels[v]
}

var notAlias = regexp.MustCompile(`[^a-z0-9._-]+`)

// alias makes a default entry name of a cloud's name ("" if it can't).
func alias(section, s string) string {
	a := strings.Trim(notAlias.ReplaceAllString(strings.ToLower(s), "-"), "-._")
	if config.CheckAlias(section, a) != nil {
		return ""
	}
	return a
}

// nameStep asks for the entry's name last, def suggesting one from the
// answers (a name given on the command line is filled in instead).
func (e *entryWizard) nameStep(f *wizard.Form, def func(wizard.Answers) string) {
	f.Input("name", "Name", def, e.checkName).Help("what you type after mek " + map[string]string{"targets": "shell", "clusters": "kube", "tunnels": "tunnel"}[e.kind.section()])
}

func (e *entryWizard) checkName(v string, _ wizard.Answers) error {
	if err := config.CheckAlias(e.kind.section(), v); err != nil {
		return err
	}
	for _, n := range e.kind.names(e.ctx) {
		if n == v && n != e.old {
			return fmt.Errorf("%s has a %s called %s — change it with: mek -c %s ctx %s edit %s", e.ctx.Name, e.kind.noun(), v, e.ctx.Name, e.kind.noun(), v)
		}
	}
	return nil
}

func cloneContext(c *config.Context) *config.Context {
	cp := *c
	cp.Clusters, cp.Targets, cp.Tunnels = maps.Clone(c.Clusters), maps.Clone(c.Targets), maps.Clone(c.Tunnels)
	return &cp
}

// changed is the context with the answers in it.
func (e *entryWizard) changed(a wizard.Answers) *config.Context {
	c := cloneContext(e.ctx)
	if e.old != "" {
		e.kind.remove(c, e.old)
	}
	e.kind.apply(e, a, c)
	return c
}

func (e *entryWizard) edited(a wizard.Answers) (*config.Editor, error) {
	ed, err := config.Open(e.ctx.Source, nil, filePerm(e.ctx.Source))
	if err != nil {
		return nil, err
	}
	c, sec, ok := e.changed(a), e.kind.section(), true
	if e.old != "" && e.old != a["name"] { // renamed
		if ok, err = ed.DeleteEntry(e.ctx.Name, sec, e.old); err != nil {
			return nil, err
		}
	}
	if ok { // the entry alone, the rest of the context (its comments) as it was
		if ok, err = ed.SetEntry(e.ctx.Name, sec, a["name"], e.kind.entry(c, a["name"])); err != nil {
			return nil, err
		}
	}
	if !ok { // written in flow style ({…}): the whole context
		err = ed.Set(e.ctx.Name, c)
	}
	return ed, err
}

func (e *entryWizard) preview(a wizard.Answers) (string, error) {
	ed, err := e.edited(a)
	if err != nil {
		return "", err
	}
	c := e.changed(a)
	entry := map[string]any{e.kind.section(): map[string]any{a["name"]: e.kind.entry(c, a["name"])}}
	return fmt.Sprintf("in %s, context %s:\n%s", config.Show(e.ctx.Source), e.ctx.Name, config.SnippetOf(entry)), checkEdit(ed)
}

func (e *entryWizard) run(title string) error {
	if err := e.prepare(); err != nil {
		return err
	}
	f := wizard.New(title)
	switch {
	case e.old != "":
		a := wizard.Answers{"name": e.old}
		e.kind.prefill(e.ctx, e.old, a)
		f.Prefill(a)
	case e.name != "":
		f.Prefill(wizard.Answers{"name": e.name})
	}
	e.kind.steps(f, e)
	a, err := runForm(f.Review("Save?", e.preview))
	if err != nil {
		return err
	}
	ed, err := e.edited(a)
	if err == nil {
		err = saveEdit(ed)
	}
	if err != nil {
		return err
	}
	verb := "added"
	if e.old != "" {
		verb = "saved"
	}
	ui.Info("%s %s %s %s to %s in %s", ui.Green("✓"), verb, e.kind.noun(), a["name"], e.ctx.Name, config.Show(e.ctx.Source))
	ui.Info("  next: %s", e.kind.next(e.ctx.Name, a["name"]))
	return nil
}

// pickEntry asks which entry to edit.
func (e *entryWizard) pickEntry() (string, error) {
	names := e.kind.names(e.ctx)
	if len(names) == 0 {
		return "", fmt.Errorf("%s has no %s yet — add one: mek -c %s ctx %s add", e.ctx.Name, e.kind.section(), e.ctx.Name, e.kind.noun())
	}
	var opts []wizard.Option
	for _, n := range names {
		opts = append(opts, wizard.Option{Value: n, Hint: e.kind.describe(e.ctx, n)})
	}
	a, err := runForm(wizard.New(e.ctx.Name).Select("name", "Which "+e.kind.noun()+"?", wizard.Options(opts...), false))
	if err != nil {
		return "", err
	}
	return a["name"], nil
}

// pickContext asks which context, this shell's ($MEK_CONTEXT) or the
// saved current one first.
func pickContext(cfg *config.Config, title string) (string, error) {
	if len(cfg.Contexts) == 0 {
		return "", errors.New("no contexts yet — add one: mek ctx add")
	}
	var opts []wizard.Option
	for _, n := range cfg.Names() {
		c := cfg.Contexts[n]
		hint := provider.For(cfg, c).Describe()
		if c.InProject {
			hint += " · " + config.Show(c.Source)
		}
		opts = append(opts, wizard.Option{Value: n, Hint: hint})
	}
	cur := os.Getenv("MEK_CONTEXT")
	if cur == "" {
		cur = config.Current()
	}
	a, err := runForm(wizard.New(title).Prefill(wizard.Answers{"name": cur}).Select("name", "Context", wizard.Options(opts...), false))
	if err != nil {
		return "", err
	}
	return a["name"], nil
}

// newEntryCmd is mek ctx <noun> add|edit|rm for one kind of entry.
func (a *app) newEntryCmd(k entryKind, short, long string) *cobra.Command {
	cmd := &cobra.Command{Use: k.noun(), Short: short, Long: long, Args: cobra.NoArgs}
	noun := k.noun()
	cmd.AddCommand(&cobra.Command{
		Use:   "add [name]",
		Short: "Add a " + noun + " to a context, a question at a time",
		Long:  long,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := needTerminal("ctx " + noun + " add"); err != nil {
				return err
			}
			e, err := a.openEntry(k)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				e.name = args[0]
			}
			return e.run("Add a " + noun + " to " + e.ctx.Name)
		},
	}, &cobra.Command{
		Use:   "edit [name]",
		Short: "Change a context's " + noun + " (no name: pick one)",
		Long:  long,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := needTerminal("ctx " + noun + " edit"); err != nil {
				return err
			}
			e, err := a.openEntry(k)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				e.old = args[0]
			} else if e.old, err = e.pickEntry(); err != nil {
				return err
			}
			if e.kind.entry(e.ctx, e.old) == nil {
				return fmt.Errorf("%s has no %s %s (have: %s)", e.ctx.Name, noun, e.old, strings.Join(k.names(e.ctx), ", "))
			}
			return e.run("Edit " + noun + " " + e.old + " of " + e.ctx.Name)
		},
	}, a.newEntryRmCmd(k))
	return cmd
}

func (a *app) newEntryRmCmd(k entryKind) *cobra.Command {
	noun := k.noun()
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a context's " + noun + " (asks first; --yes doesn't)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if a.opts.context == "" && a.opts.yes { // no list to pick from: this shell's context, if it has one
				a.opts.context = os.Getenv("MEK_CONTEXT")
			}
			if a.opts.context == "" && !a.opts.yes {
				if err := needTerminal("ctx " + noun + " rm without -c"); err != nil {
					return err
				}
			} else if a.opts.context == "" {
				return fmt.Errorf("which context? mek -c <context> ctx %s rm %s --yes (or mek use -s <context> first)", noun, args[0])
			}
			e, err := a.openEntry(k)
			if err != nil {
				return err
			}
			name := args[0]
			if k.entry(e.ctx, name) == nil {
				return fmt.Errorf("%s has no %s %s (have: %s)", e.ctx.Name, noun, name, strings.Join(k.names(e.ctx), ", "))
			}
			if !a.opts.yes { // -y / --yes
				ok, err := confirm(fmt.Sprintf("Remove %s %s from %s (%s)?", noun, name, e.ctx.Name, config.Show(e.ctx.Source)))
				if errors.Is(err, ui.ErrNoTTY) {
					return errors.New("no terminal to ask on — pass --yes to remove it")
				}
				if err != nil || !ok {
					return err
				}
			}
			c := cloneContext(e.ctx)
			k.remove(c, name)
			if err := editConfig(e.ctx.Source, nil, func(ed *config.Editor) error {
				if ok, err := ed.DeleteEntry(e.ctx.Name, k.section(), name); ok || err != nil {
					return err
				}
				return ed.Set(e.ctx.Name, c) // written in flow style: the whole context
			}); err != nil {
				return err
			}
			ui.Info("%s removed %s %s from %s", ui.Green("✓"), noun, name, e.ctx.Name)
			return nil
		},
	}
	return cmd
}

// Which context: -c, or picked from a list (the current one first). The
// entry is saved in the file the context comes from.
const entryContext = `Which context: -c <context>, or without it a list to pick from (the current
one first). The change goes in the file the context comes from; the rest of
that file stays as it was, and nothing is saved that would make the config
invalid.

` + wizardKeys

const targetHelp = `Targets are a context's hosts for mek shell, and hops for its tunnels.

  add    lists the running instances — EC2 with their Name tag and private IP,
         or GCP VMs with their zone — then asks:
           AWS  refer to it by its Name tag (still right when the instance is
                replaced) or by its id
           GCP  the zone (filled in) and the SSH user (empty: gcloud's)
         and a name (filled in from the instance's)
  edit   the same, today's answers filled in (no name: pick one)
  rm     removes it (asks first; --yes doesn't, with -c)

` + entryContext + `

  mek -c prod ctx target add            # pick an instance
  mek -c prod ctx target add bastion    # the name filled in
  mek ctx target edit                   # pick the context, then the target
  mek -c prod ctx target rm bastion --yes

Then: mek -c prod shell bastion`

const clusterHelp = `Clusters are a context's Kubernetes clusters for mek kube and mek kubectl.

  add    lists the clusters — EKS in the context's region (asked when it has
         none), or GKE in every location — then asks:
           GCP  the location (filled in) and how to reach it: its endpoint,
                its internal IP (private) or its DNS-based endpoint (dns)
         the default namespace, and a name (filled in from the cluster's)
  edit   the same, today's answers filled in (no name: pick one)
  rm     removes it (asks first; --yes doesn't, with -c)

` + entryContext + `

  mek -c dev ctx cluster add            # pick a cluster
  mek -c dev ctx cluster edit main      # e.g. another namespace
  mek -c dev ctx cluster rm old --yes

Then: mek -c dev kube main`

const tunnelHelp = `Tunnels are a context's port forwards for mek tunnel.

  add    asks what to forward to:
           a host, through an instance   e.g. a database. The instance is one
                                         of the context's targets or a running
                                         instance (listed); on AWS, RDS and
                                         Aurora endpoints are listed, with
                                         their port
           a port on an instance         a service on the instance itself
           Cloud SQL (GCP)               the instances are listed; over the
                                         public or the private IP
         then the local port (filled in: the port + 10000; one in use here
         isn't taken) and a name
  edit   the same, today's answers filled in (no name: pick one)
  rm     removes it (asks first; --yes doesn't, with -c)

` + entryContext + `

  mek -c dev ctx tunnel add             # e.g. RDS through a bastion
  mek -c dev ctx tunnel add db          # the name filled in
  mek -c dev ctx tunnel edit db         # e.g. another local port
  mek -c dev ctx tunnel rm db --yes

Then: mek -c dev tunnel db`
