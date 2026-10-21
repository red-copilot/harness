package harness

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skills/tsec-submit/SKILL.md
//go:embed skills/tsec-submit/references/CHALLENGES_API.md
//go:embed skills/tools/SKILL.md
//go:embed skills/tools/references/*.md
var embeddedSkills embed.FS

func prepareEmbeddedSkills(dst string) error {
	return fs.WalkDir(embeddedSkills, "skills", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "skills" {
			return nil
		}
		relative := strings.TrimPrefix(path, "skills/")
		target := filepath.Join(dst, filepath.FromSlash(relative))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := embeddedSkills.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write embedded skill %s: %w", relative, err)
		}
		return nil
	})
}
