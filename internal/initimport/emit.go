package initimport

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Emit(plan Plan, dir string) error {
	agentDir := filepath.Join(dir, "agents", plan.AgentName)
	targets := []string{
		filepath.Join(agentDir, "Clawfile"),
		filepath.Join(agentDir, "AGENTS.md"),
		filepath.Join(dir, "claw-pod.yml"),
		filepath.Join(dir, ".env.example"),
		filepath.Join(dir, "MIGRATION.md"),
	}
	if plan.SoulContent != "" {
		targets = append(targets, filepath.Join(agentDir, "SOUL.md"))
	}
	for _, target := range targets {
		if _, err := os.Stat(target); err == nil {
			rel, _ := filepath.Rel(dir, target)
			return fmt.Errorf("%s already exists; refusing to overwrite (delete it first or use a new directory)", filepath.ToSlash(rel))
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check existing %s: %w", target, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "skills"), 0o755); err != nil {
		return fmt.Errorf("create agent directory: %w", err)
	}

	files := map[string]string{
		filepath.Join(agentDir, "Clawfile"):  renderClawfile(plan),
		filepath.Join(agentDir, "AGENTS.md"): plan.AgentContract,
		filepath.Join(dir, "claw-pod.yml"):   renderPod(plan),
		filepath.Join(dir, ".env.example"):   renderEnvExample(plan),
		filepath.Join(dir, "MIGRATION.md"):   renderMigration(plan),
	}
	if plan.SoulContent != "" {
		files[filepath.Join(agentDir, "SOUL.md")] = plan.SoulContent
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if plan.SkillsDir != "" {
		if err := copyDirFiles(plan.SkillsDir, filepath.Join(agentDir, "skills")); err != nil {
			return fmt.Errorf("copy skills: %w", err)
		}
	}
	if plan.CronDir != "" {
		if err := copyDirFiles(plan.CronDir, filepath.Join(dir, "imported", "cron")); err != nil {
			return fmt.Errorf("copy cron references: %w", err)
		}
	}
	if err := writeGitignore(filepath.Join(dir, ".gitignore")); err != nil {
		return err
	}
	return nil
}

func renderClawfile(plan Plan) string {
	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(plan.BaseImage)
	b.WriteString("\n\n")
	b.WriteString("CLAW_TYPE ")
	b.WriteString(string(plan.Target))
	b.WriteString("\n")
	b.WriteString("AGENT AGENTS.md\n\n")
	b.WriteString("MODEL primary ")
	b.WriteString(plan.Model.String())
	b.WriteString("\n")
	for _, fallback := range plan.Fallback {
		b.WriteString("MODEL fallback ")
		b.WriteString(fallback.String())
		b.WriteString("\n")
	}
	if plan.Cllama {
		b.WriteString("\nCLLAMA passthrough\n")
	}
	for _, handle := range plan.Handles {
		b.WriteString("\nHANDLE ")
		b.WriteString(handle.Platform)
		b.WriteString("\n")
	}
	return b.String()
}

func renderPod(plan Plan) string {
	var b strings.Builder
	b.WriteString("x-claw:\n")
	b.WriteString("  pod: ")
	b.WriteString(plan.ProjectName)
	b.WriteString("\n")
	b.WriteString("services:\n")
	b.WriteString("  ")
	b.WriteString(plan.AgentName)
	b.WriteString(":\n")
	b.WriteString("    image: ")
	b.WriteString(plan.ProjectName)
	b.WriteString("-")
	b.WriteString(plan.AgentName)
	b.WriteString(":latest\n")
	b.WriteString("    build:\n")
	b.WriteString("      context: ./agents/")
	b.WriteString(plan.AgentName)
	b.WriteString("\n")
	b.WriteString("    x-claw:\n")
	b.WriteString("      agent: ./agents/")
	b.WriteString(plan.AgentName)
	b.WriteString("/AGENTS.md\n")
	if plan.Cllama {
		b.WriteString("      cllama: passthrough\n")
		if len(plan.CllamaEnv) > 0 {
			b.WriteString("      cllama-env:\n")
			for _, key := range sortedMapKeys(plan.CllamaEnv) {
				b.WriteString("        ")
				b.WriteString(key)
				b.WriteString(": \"")
				b.WriteString(plan.CllamaEnv[key])
				b.WriteString("\"\n")
			}
		}
	}
	if len(plan.Handles) > 0 {
		b.WriteString("      handles:\n")
		for _, handle := range plan.Handles {
			b.WriteString("        ")
			b.WriteString(handle.Platform)
			b.WriteString(":\n")
			b.WriteString("          id: \"${")
			b.WriteString(handle.IDEnv)
			b.WriteString("}\"\n")
			b.WriteString("          username: \"")
			b.WriteString(plan.AgentName)
			b.WriteString("\"\n")
			if handle.Platform == "discord" && len(handle.Guilds) > 0 {
				b.WriteString("          guilds:\n")
				for _, guild := range handle.Guilds {
					b.WriteString("            - id: \"")
					b.WriteString(guild.ID)
					b.WriteString("\"\n")
				}
			}
		}
	}
	if len(plan.Surfaces) > 0 {
		b.WriteString("      surfaces:\n")
		for _, surface := range plan.Surfaces {
			if surface.Platform == "discord" && surface.Discord != nil {
				renderDiscordSurface(&b, surface.Discord)
			}
		}
	}
	env := serviceEnvironment(plan)
	if len(env) > 0 {
		b.WriteString("    environment:\n")
		for _, key := range sortedMapKeys(env) {
			b.WriteString("      ")
			b.WriteString(key)
			b.WriteString(": \"")
			b.WriteString(env[key])
			b.WriteString("\"\n")
		}
	}
	return b.String()
}

func renderDiscordSurface(b *strings.Builder, surface *DiscordSurface) {
	b.WriteString("        - channel://discord:\n")
	if surface.DMPolicy != "" || len(surface.DMAllowFrom) > 0 {
		b.WriteString("            dm:\n")
		if surface.DMPolicy != "" {
			b.WriteString("              policy: ")
			b.WriteString(surface.DMPolicy)
			b.WriteString("\n")
		}
		if len(surface.DMAllowFrom) > 0 {
			b.WriteString("              allow_from:\n")
			for _, id := range surface.DMAllowFrom {
				b.WriteString("                - \"")
				b.WriteString(id)
				b.WriteString("\"\n")
			}
		}
	}
	if len(surface.Guilds) > 0 {
		b.WriteString("            guilds:\n")
		for _, guild := range surface.Guilds {
			b.WriteString("              \"")
			b.WriteString(guild.ID)
			b.WriteString("\":\n")
			if guild.RequireMention {
				b.WriteString("                require_mention: true\n")
			}
			if len(guild.Users) > 0 {
				b.WriteString("                users:\n")
				for _, user := range guild.Users {
					b.WriteString("                  - \"")
					b.WriteString(user)
					b.WriteString("\"\n")
				}
			}
		}
	}
}

func renderEnvExample(plan Plan) string {
	keys := map[string]struct{}{}
	for key := range plan.Environment {
		keys[key] = struct{}{}
	}
	for key := range plan.CllamaEnv {
		keys[key] = struct{}{}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	var b strings.Builder
	for _, key := range out {
		b.WriteString(key)
		b.WriteString("=\n")
	}
	return b.String()
}

func renderMigration(plan Plan) string {
	var b strings.Builder
	b.WriteString("# Migration notes -- generated by `claw init --from`\n\n")
	b.WriteString("Source: ")
	b.WriteString(string(plan.Source.Kind))
	b.WriteString(" (config: ")
	b.WriteString(plan.Source.Config)
	b.WriteString(")\n")
	b.WriteString("Target: ")
	b.WriteString(string(plan.Target))
	b.WriteString("\n\n")
	writeList := func(title string, values []string) {
		b.WriteString("## ")
		b.WriteString(title)
		b.WriteString("\n\n")
		if len(values) == 0 {
			b.WriteString("- None\n\n")
			return
		}
		for _, value := range values {
			b.WriteString("- ")
			b.WriteString(value)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	writeList("Applied", uniqueSorted(plan.Notes.Applied))
	actions := append([]string(nil), plan.Notes.Action...)
	writeList("Action required", uniqueSorted(actions))
	writeList("Secret placeholders", uniqueSorted(plan.Notes.SecretNotes))
	verify := make([]string, 0)
	for _, key := range sortedMapKeys(plan.Environment) {
		if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") || strings.HasSuffix(key, "_ID") {
			verify = append(verify, key+" is set in .env")
		}
	}
	writeList("Verify before `claw up`", uniqueSorted(verify))
	return b.String()
}

func serviceEnvironment(plan Plan) map[string]string {
	out := make(map[string]string, len(plan.Environment))
	for key, value := range plan.Environment {
		if _, cllamaOnly := plan.CllamaEnv[key]; cllamaOnly {
			continue
		}
		out[key] = value
	}
	return out
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeGitignore(path string) error {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			seen[strings.TrimSpace(line)] = struct{}{}
		}
	}
	for _, entry := range []string{".env", "*.generated.*", "imported/"} {
		if _, ok := seen[entry]; !ok {
			lines = append(lines, entry)
			seen[entry] = struct{}{}
		}
	}
	output := strings.Join(lines, "\n")
	output = strings.TrimLeft(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}
