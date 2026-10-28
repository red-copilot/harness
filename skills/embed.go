package skills

import "embed"

//go:embed tsec-submit/SKILL.md
//go:embed tsec-submit/references/CHALLENGES_API.md
//go:embed tools/SKILL.md
//go:embed tools/references/*.md
var FS embed.FS
