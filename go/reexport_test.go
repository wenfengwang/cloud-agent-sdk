package cloudagent_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestReexportsCore fails when an exported identifier of package core has no
// counterpart in package cloudagent, or the other way around, so the portal
// never drifts from the implementation.
func TestReexportsCore(t *testing.T) {
	core := exportedNames(t, filepath.Join("..", "core"))
	portal := exportedNames(t, ".")
	if missing := difference(core, portal); len(missing) > 0 {
		t.Errorf("cloudagent does not re-export: %s", strings.Join(missing, ", "))
	}
	if extra := difference(portal, core); len(extra) > 0 {
		t.Errorf("cloudagent exports identifiers core does not have: %s", strings.Join(extra, ", "))
	}
}

// exportedNames returns the exported top-level identifiers declared in the
// non-test files of the package in dir. Methods are excluded: aliases carry
// them along.
func exportedNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							names[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.IsExported() {
								names[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return names
}

func difference(a, b map[string]bool) []string {
	var out []string
	for name := range a {
		if !b[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
