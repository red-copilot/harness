package harness_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRootPackageImportsStdlibOnly(t *testing.T) {
	entries, err := osReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		for _, path := range fileImports(filepath.Join(".", entry.Name())) {
			if strings.Contains(strings.Split(path, "/")[0], ".") {
				t.Fatalf("root file %s imports non-stdlib %q", entry.Name(), path)
			}
		}
	}
}

func TestAdapterPackagesDoNotImportControlLoop(t *testing.T) {
	for _, root := range []string{"pi", "sandbox/docker", "tsec", "verify", "store/sqlite"} {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			for _, imported := range fileImports(path) {
				for _, forbidden := range []string{"harness/server", "harness/internal/cli", "harness/webui", "harness/exploration"} {
					if imported == forbidden {
						t.Fatalf("%s imports forbidden control surface %q", path, imported)
					}
				}
			}
			return nil
		})
	}
}

func TestNoLegacyControlSymbols(t *testing.T) {
	forbidden := map[string]bool{"Workflow": true, "WorkflowRuntime": true, "PiSpec": true, "KnowledgeVerified": true, "AgentResult": true, "SessionFactory": true, "DefaultFactory": true}
	_ = filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == ".superpowers" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			value, _ := strconv.Unquote(imported.Path.Value)
			if value == "github.com/red-copilot/harness/exploration" {
				t.Fatalf("%s imports removed exploration package", path)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && forbidden[ident.Name] {
				t.Fatalf("%s contains legacy symbol %s", path, ident.Name)
			}
			return true
		})
		return nil
	})
}

func fileImports(path string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	var imports []string
	for _, spec := range file.Imports {
		value, _ := strconv.Unquote(spec.Path.Value)
		imports = append(imports, value)
	}
	return imports
}

func osReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
