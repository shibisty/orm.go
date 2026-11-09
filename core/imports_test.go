package orm

import (
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCoreImportsNoDriver keeps the core independent of its drivers: the
// orm package and its subpackages import only the standard library and
// themselves, so a project never pulls in a database client it does not use
// (drivers depend on orm, never the other way round).
func TestCoreImportsNoDriver(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "gtr_modules" || strings.HasPrefix(d.Name(), ".") && path != ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "orm" || strings.HasPrefix(p, "orm/") {
				continue
			}
			if pkg, err := build.Import(p, "", build.FindOnly); err == nil && pkg.Goroot {
				continue
			}
			t.Errorf("%s imports %q: the core may import only the standard library and orm itself", path, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 5 {
		t.Fatalf("only %d files checked", checked)
	}
}
