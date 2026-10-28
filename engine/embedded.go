package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	embedded "github.com/red-copilot/harness/skills"
)

func prepareEmbeddedSkills(dst string) error {
	return fs.WalkDir(embedded.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := embedded.FS.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write embedded skill %s: %w", strings.TrimPrefix(path, "./"), err)
		}
		return nil
	})
}
