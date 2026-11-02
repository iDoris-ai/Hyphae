package common

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestStableErrorCodesCoverConstants fails when an ErrCode* constant in
// output.go is missing from StableErrorCodes (and vice versa).
func TestStableErrorCodesCoverConstants(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "output.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{} // const name -> value
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if strings.HasPrefix(n.Name, "ErrCode") {
					declared[n.Name] = strings.Trim(vs.Values[i].(*ast.BasicLit).Value, `"`)
				}
			}
		}
	}
	if len(declared) < 10 {
		t.Fatalf("parsed only %d ErrCode constants", len(declared))
	}
	listed := map[string]bool{}
	for _, c := range StableErrorCodes {
		listed[c] = true
	}
	for name, val := range declared {
		if !listed[val] {
			t.Errorf("%s (%q) is missing from StableErrorCodes", name, val)
		}
	}
	if len(listed) != len(declared) || len(StableErrorCodes) != len(declared) {
		t.Errorf("StableErrorCodes has %d entries (%d unique), output.go declares %d", len(StableErrorCodes), len(listed), len(declared))
	}
}
