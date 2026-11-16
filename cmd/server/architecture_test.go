package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitecture_ProductionStateIsNotPackageGlobal(t *testing.T) {
	// AGENTS.md requires runtime state in structs. Compiler embeds and blank
	// interface assertions are declarations rather than mutable runtime state.
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join("..", "..", directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			if ast.IsGenerated(file) {
				return nil
			}
			for _, declaration := range file.Decls {
				group, ok := declaration.(*ast.GenDecl)
				if !ok || group.Tok != token.VAR {
					continue
				}
				for _, specification := range group.Specs {
					value := specification.(*ast.ValueSpec)
					embedded := false
					if group.Doc != nil {
						for _, comment := range group.Doc.List {
							embedded = embedded || strings.HasPrefix(comment.Text, "//go:embed ")
						}
					}
					if selector, ok := value.Type.(*ast.SelectorExpr); ok && selector.Sel.Name == "FS" {
						if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "embed" && embedded {
							continue
						}
					}
					for _, name := range value.Names {
						if name.Name != "_" {
							t.Errorf("%s: %s is package-level state; use a struct, constant or local value", path, name.Name)
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
