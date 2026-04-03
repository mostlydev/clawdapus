package main

import "embed"

//go:generate sh -c "mkdir -p skill_data && cp ../../skills/clawdapus/SKILL.md skill_data/SKILL.md"

//go:embed skill_data/SKILL.md
var embeddedSkillFS embed.FS

func embeddedSkillContent() ([]byte, error) {
	return embeddedSkillFS.ReadFile("skill_data/SKILL.md")
}
