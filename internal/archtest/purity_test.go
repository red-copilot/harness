// Package archtest enforces dependency and purity rules that cannot be
// expressed by Go's type system.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files
}

// TestKernelHasNoAmbientTimeRandomnessOrEnvironment keeps the kernel
// deterministic: clocks, randomness and the process environment are injected
// ports, not ambient calls. Filesystem purity is enforced separately as the
// Workspace port lands.
func TestKernelHasNoAmbientTimeRandomnessOrEnvironment(t *testing.T) {
	root := repoRoot(t)
	for _, pkg := range []string{"core", "engine"} {
		for _, path := range nonTestGoFiles(t, filepath.Join(root, pkg)) {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			rel, _ := filepath.Rel(root, path)
			for _, imp := range file.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if importPath == "crypto/rand" {
					t.Errorf("%s imports crypto/rand; use the IDSource port", rel)
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.SelectorExpr:
					ident, ok := n.X.(*ast.Ident)
					if !ok {
						return true
					}
					switch {
					case ident.Name == "time" && n.Sel.Name == "Now":
						t.Errorf("%s calls time.Now; use the Clock port", rel)
					case ident.Name == "os" && n.Sel.Name == "LookupEnv":
						t.Errorf("%s calls os.LookupEnv; use the SecretSource port", rel)
					}
				case *ast.Ident:
					if pkg == "engine" && (n.Name == "ProcessStarter" || n.Name == "ResourceCleaner") {
						t.Errorf("%s references %s; process lifecycle belongs to adapters", rel, n.Name)
					}
				}
				return true
			})
		}
	}
}
