package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prateep-r/mek/internal/catalog"
	"github.com/prateep-r/mek/internal/config"
	"github.com/prateep-r/mek/internal/guard"
)

var loadCatalog = catalog.Load // test seam

// Kind is a kind of resource, as the window lists it.
type Kind struct {
	Service, Name string
	Actions       []string // their commands' last words: start, stop, delete
}

// Catalog is a context's cloud: its kinds of resource, by service.
type Catalog struct {
	Cloud, Source string
	Kinds         []Kind
}

// Resources is the catalog of a context's cloud.
func (s *Service) Resources(ctx string) (Catalog, error) {
	c := s.context(ctx)
	if c == nil {
		return Catalog{}, fmt.Errorf("no context %q", ctx)
	}
	cat, err := loadCatalog(c.Cloud)
	if err != nil {
		return Catalog{}, err
	}
	out := Catalog{Cloud: cat.Cloud, Source: cat.Source}
	for _, svc := range cat.Services {
		for _, r := range svc.Resources {
			k := Kind{Service: svc.Name, Name: r.Name}
			for _, a := range r.Actions {
				f := strings.Fields(a.Command)
				k.Actions = append(k.Actions, f[len(f)-1])
			}
			out.Kinds = append(out.Kinds, k)
		}
	}
	return out, nil
}

// Page is a page of a list: its rows, and what fetches the next ("" when
// there is none).
type Page struct {
	Rows []catalog.Row
	Next string
}

// resource finds a kind of resource in a context's catalog, and its Source.
func (s *Service) resource(ctx, service, name string) (catalog.Source, *catalog.Service, *catalog.Resource, error) {
	c := s.context(ctx)
	if c == nil {
		return nil, nil, nil, fmt.Errorf("no context %q", ctx)
	}
	cat, err := loadCatalog(c.Cloud)
	if err != nil {
		return nil, nil, nil, err
	}
	src, err := catalog.For(c.Cloud)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range cat.Services {
		svc := &cat.Services[i]
		if svc.Name != service {
			continue
		}
		for j := range svc.Resources {
			if svc.Resources[j].Name == name {
				return src, svc, &svc.Resources[j], nil
			}
		}
	}
	return nil, nil, nil, fmt.Errorf("%s has no %s %s", c.Cloud, service, name)
}

// List lists a kind of resource: the first page, or the one next names.
func (s *Service) List(ctx, service, name, next string) (Page, error) {
	src, svc, r, err := s.resource(ctx, service, name)
	if err != nil {
		return Page{}, err
	}
	out, err := run(s.mek, append([]string{"-c", ctx}, src.List(svc, r, next)...))
	if err != nil {
		return Page{}, err
	}
	rows, more, err := src.Rows(r, out)
	return Page{Rows: rows, Next: more}, err
}

// Describe is one resource's details as indented JSON: its describe
// command's output, or its row's when there is none.
func (s *Service) Describe(ctx, service, name string, row catalog.Row) (string, error) {
	src, svc, r, err := s.resource(ctx, service, name)
	if err != nil {
		return "", err
	}
	text := []byte(row.Raw)
	if argv := src.Describe(svc, r, row); argv != nil {
		if text, err = run(s.mek, append([]string{"-c", ctx}, argv...)); err != nil {
			return "", err
		}
	}
	var b bytes.Buffer
	if json.Indent(&b, text, "", "  ") != nil {
		return strings.TrimSpace(string(text)), nil
	}
	return b.String(), nil
}

// Act plans an action on one resource: always asked first, with the
// context's name typed for a destructive command on a protected context;
// on a readonly context mek blocks it, and the audit log has the attempt.
func (s *Service) Act(ctx, service, name, action string, row catalog.Row) (Plan, error) {
	src, svc, r, err := s.resource(ctx, service, name)
	if err != nil {
		return Plan{}, err
	}
	for _, a := range r.Actions {
		if f := strings.Fields(a.Command); f[len(f)-1] != action {
			continue
		}
		c := s.context(ctx)
		argv := src.Act(svc, r, a, row)
		p := Plan{Label: fmt.Sprintf("%s %s (%s)", a.Command, cmpStr(row.Name, row.ID), ctx), Args: append([]string{"-c", ctx}, argv...)}
		class := classify(c.Cloud, argv[1:])
		switch guard.Decide(&config.Context{Protected: c.Protected, ReadOnly: c.ReadOnly}, class) {
		case guard.Block:
		case guard.ConfirmTyped:
			p.Ask, p.Typed, p.Text = "typed", ctx, fmt.Sprintf("%s is protected — %s: %s", ctx, class, strings.Join(argv, " "))
		default:
			p.Ask, p.Text = "yes", fmt.Sprintf("%s: %s?", class, strings.Join(argv, " "))
		}
		return p, nil
	}
	return Plan{}, fmt.Errorf("%s %s has no action %q", service, name, action)
}

func classify(cloud string, args []string) guard.Class {
	switch cloud {
	case "gcp":
		return guard.ClassifyGCloud(args)
	case "azure":
		return guard.ClassifyAzure(args)
	}
	return guard.ClassifyAWS(args)
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
