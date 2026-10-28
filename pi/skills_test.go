package pi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeSkillsCopiesToolCatalog(t *testing.T) {
	dst := t.TempDir()
	if err := MaterializeSkills(dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tools/SKILL.md", "tools/references/nmap.md", "tsec-submit/SKILL.md"} {
		data, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil || len(data) == 0 {
			t.Fatalf("read embedded skill %s: %v", name, err)
		}
	}
}
