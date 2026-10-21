package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareEmbeddedSkillsCopiesToolCatalog(t *testing.T) {
	dst := t.TempDir()
	if err := prepareEmbeddedSkills(dst); err != nil {
		t.Fatalf("prepareEmbeddedSkills: %v", err)
	}

	for _, name := range []string{
		"tools/SKILL.md",
		"tools/references/nmap.md",
		"tools/references/ffuf.md",
		"tools/references/fscan.md",
		"tools/references/sqlmap.md",
		"tools/references/hydra.md",
		"tools/references/sshpass.md",
		"tools/references/netcat.md",
		"tools/references/ncat.md",
		"tools/references/socat.md",
		"tools/references/chisel.md",
		"tools/references/proxychains4.md",
		"tsec-submit/SKILL.md",
	} {
		data, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read embedded skill %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded skill %s is empty", name)
		}
	}
}
