package main

import (
	"go/ast"
	"go/doc"
	"go/token"
)

// Undocumented lists, for each public package, its exported identifiers
// that have no doc comment: the package itself ("package"), constants and
// variables (a group's comment documents every name in it; without one,
// each name needs its own, or a comment at the end of its line), functions,
// types, and methods (as Type.Method).
func (m *Module) Undocumented() map[string][]string {
	out := map[string][]string{}
	for _, p := range m.Packages {
		if miss := undocumented(p.Doc); len(miss) > 0 {
			out[p.ImportPath] = miss
		}
	}
	return out
}

func undocumented(p *doc.Package) []string {
	var miss []string
	if p.Doc == "" {
		miss = append(miss, "package")
	}
	values := func(vs []*doc.Value) {
		for _, v := range vs {
			if v.Doc != "" {
				continue
			}
			for _, s := range v.Decl.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok || vs.Doc != nil || vs.Comment != nil {
					continue
				}
				for _, n := range vs.Names {
					if token.IsExported(n.Name) {
						miss = append(miss, n.Name)
					}
				}
			}
		}
	}
	funcs := func(prefix string, fs []*doc.Func) {
		for _, f := range fs {
			if f.Doc == "" {
				miss = append(miss, prefix+f.Name)
			}
		}
	}
	values(p.Consts)
	values(p.Vars)
	funcs("", p.Funcs)
	for _, t := range p.Types {
		if t.Doc == "" {
			miss = append(miss, t.Name)
		}
		values(t.Consts)
		values(t.Vars)
		funcs("", t.Funcs)
		funcs(t.Name+".", t.Methods)
	}
	return miss
}
