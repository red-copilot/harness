package harness_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/red-copilot/harness"

func imports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func TestCoreImportsStdlibOnly(t *testing.T) {
	entries, err := filepath.Glob("core/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		paths, err := imports(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range paths {
			if strings.Contains(strings.Split(imported, "/")[0], ".") {
				t.Fatalf("core file %s imports non-stdlib %q", path, imported)
			}
		}
	}
}

func TestEngineDoesNotImportAdaptersOrSurfaces(t *testing.T) {
	forbidden := []string{
		modulePath + "/pi",
		modulePath + "/sandbox",
		modulePath + "/tsec",
		modulePath + "/verify",
		modulePath + "/store",
		modulePath + "/server",
		modulePath + "/internal/cli",
		modulePath + "/webui",
	}
	entries, err := filepath.Glob("engine/*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		paths, err := imports(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range paths {
			for _, bad := range forbidden {
				if imported == bad || strings.HasPrefix(imported, bad+"/") {
					t.Fatalf("%s imports forbidden control surface %q", path, imported)
				}
			}
		}
	}
}

func TestAdaptersDoNotImportSurfaces(t *testing.T) {
	forbidden := []string{modulePath + "/server", modulePath + "/internal/cli", modulePath + "/webui"}
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
			paths, err := imports(path)
			if err != nil {
				return err
			}
			for _, imported := range paths {
				for _, bad := range forbidden {
					if imported == bad || strings.HasPrefix(imported, bad+"/") {
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
		t.Fatal("no adapter files inspected")
	}
}
