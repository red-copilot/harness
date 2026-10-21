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

// modulePath is the import prefix every package in this repository shares.
const modulePath = "github.com/red-copilot/harness"

func TestRootPackageImportsStdlibOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		imports, err := fileImports(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range imports {
			if strings.Contains(strings.Split(path, "/")[0], ".") {
				t.Fatalf("root file %s imports non-stdlib %q", entry.Name(), path)
			}
		}
	}
}

// TestAdapterPackagesDoNotImportControlLoop proves adapter packages cannot
// reach the control surface. Import paths are compared against the full module
// prefix so the check cannot silently stop matching.
func TestAdapterPackagesDoNotImportControlLoop(t *testing.T) {
	forbidden := []string{
		modulePath + "/server",
		modulePath + "/internal/cli",
		modulePath + "/webui",
		modulePath + "/exploration",
	}
	checked := 0
	for _, root := range []string{"pi", "sandbox/docker", "tsec", "verify", "store/sqlite"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			checked++
			imports, err := fileImports(path)
			if err != nil {
				return err
			}
			for _, imported := range imports {
				for _, forbiddenPath := range forbidden {
					if imported == forbiddenPath || strings.HasPrefix(imported, forbiddenPath+"/") {
						t.Fatalf("%s imports forbidden control surface %q", path, imported)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no adapter files were inspected")
	}
}

func TestNoLegacyControlSymbols(t *testing.T) {
	forbidden := map[string]bool{"Workflow": true, "WorkflowRuntime": true, "PiSpec": true, "KnowledgeVerified": true, "AgentResult": true, "SessionFactory": true, "DefaultFactory": true}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
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
			if value == modulePath+"/exploration" {
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
	if err != nil {
		t.Fatal(err)
	}
}

func fileImports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var imports []string
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	return imports, nil
}
