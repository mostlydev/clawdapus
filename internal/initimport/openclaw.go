package initimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func readOpenClaw(configPath string) (Descriptor, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Descriptor{}, fmt.Errorf("read openclaw config: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Descriptor{}, fmt.Errorf("parse openclaw.json: %w", err)
	}

	root := filepath.Dir(configPath)
	if filepath.Base(root) == "config" {
		root = filepath.Dir(root)
	}
	env, err := readDotEnv(filepath.Join(root, ".env"))
	if err != nil {
		return Descriptor{}, fmt.Errorf("read source .env: %w", err)
	}

	desc := Descriptor{
		Kind:      SourceOpenClaw,
		Config:    configPath,
		Root:      root,
		AgentName: "assistant",
		EnvVars:   env,
	}

	if agents, ok := raw["agents"].(map[string]any); ok {
		if list, ok := agents["list"].([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				if id, ok := first["id"].(string); ok && strings.TrimSpace(id) != "" {
					desc.AgentName = normalizeName(id, "assistant")
				} else if name, ok := first["name"].(string); ok && strings.TrimSpace(name) != "" {
					desc.AgentName = normalizeName(name, "assistant")
				}
				if name, ok := first["name"].(string); ok && strings.TrimSpace(name) != "" {
					desc.Identity = strings.TrimSpace(name)
				}
			}
		}
	}

	if primary, ok := nestedString(raw, "agents", "defaults", "model", "primary"); ok {
		if model, split := SplitModelRef(primary); split {
			desc.Models.Primary = model
		} else {
			desc.Models.Primary = ModelRef{Provider: primary}
		}
	}
	if fallbacks := stringSlice(nestedValue(raw, "agents", "defaults", "model", "fallbacks")); len(fallbacks) > 0 {
		for _, fallback := range fallbacks {
			if model, ok := SplitModelRef(fallback); ok {
				desc.Models.Fallback = append(desc.Models.Fallback, model)
			}
		}
	}
	if desc.Models.Primary.Provider == "" {
		desc.Models.Primary = ModelRef{Provider: "openrouter", Model: "anthropic/claude-sonnet-5"}
	}
	if providers, ok := nestedMap(raw, "models", "providers"); ok {
		if providerCfg, ok := providers[desc.Models.Primary.Provider].(map[string]any); ok {
			if baseURL, ok := providerCfg["baseUrl"].(string); ok && strings.TrimSpace(baseURL) != "" {
				desc.Models.Primary.BaseURL = strings.TrimSpace(baseURL)
				desc.Cllama = true
			}
			if apiKey, ok := providerCfg["apiKey"].(string); ok {
				desc.Models.Primary.APIKey = strings.TrimSpace(apiKey)
			}
		}
	}

	channels, _ := raw["channels"].(map[string]any)
	desc.Channels.Discord = readOpenClawDiscord(channels)
	desc.Channels.Slack = readOpenClawSlack(channels)
	desc.Channels.Telegram = readOpenClawTelegram(channels)

	if dir := filepath.Join(root, "skills"); dirExists(dir) {
		desc.SkillsDir = dir
	}
	return desc, nil
}

func readOpenClawDiscord(channels map[string]any) *DiscordChannel {
	raw, ok := channels["discord"].(map[string]any)
	if !ok {
		return nil
	}
	if enabled, ok := raw["enabled"].(bool); ok && !enabled {
		return nil
	}
	d := &DiscordChannel{}
	if token, ok := raw["token"].(string); ok {
		d.Token = strings.TrimSpace(token)
	}
	if dmPolicy, ok := raw["dmPolicy"].(string); ok {
		d.DMPolicy = strings.TrimSpace(dmPolicy)
	}
	if require, ok := raw["requireMention"].(bool); ok {
		d.RequireMention = require
	}
	d.AllowFrom = stringSlice(raw["allowFrom"])
	if guilds, ok := raw["guilds"].(map[string]any); ok {
		for id, guildRaw := range guilds {
			guildMap, ok := guildRaw.(map[string]any)
			if !ok {
				continue
			}
			g := DiscordGuild{ID: id, Users: stringSlice(guildMap["users"])}
			if require, ok := guildMap["requireMention"].(bool); ok {
				g.RequireMention = require
				if require {
					d.RequireMention = true
				}
			}
			d.Guilds = append(d.Guilds, g)
		}
		sort.Slice(d.Guilds, func(i, j int) bool {
			return d.Guilds[i].ID < d.Guilds[j].ID
		})
	}
	return d
}

func readOpenClawSlack(channels map[string]any) *SlackChannel {
	raw, ok := channels["slack"].(map[string]any)
	if !ok {
		return nil
	}
	if enabled, ok := raw["enabled"].(bool); ok && !enabled {
		return nil
	}
	s := &SlackChannel{}
	if token, ok := raw["token"].(string); ok {
		s.BotToken = strings.TrimSpace(token)
	}
	s.AllowedUsers = append(s.AllowedUsers, stringSlice(raw["allowFrom"])...)
	s.AllowedUsers = append(s.AllowedUsers, stringSlice(raw["allowedUsers"])...)
	return s
}

func readOpenClawTelegram(channels map[string]any) *TelegramChannel {
	raw, ok := channels["telegram"].(map[string]any)
	if !ok {
		return nil
	}
	if enabled, ok := raw["enabled"].(bool); ok && !enabled {
		return nil
	}
	t := &TelegramChannel{}
	if token, ok := raw["token"].(string); ok {
		t.Token = strings.TrimSpace(token)
	}
	return t
}

func nestedValue(m map[string]any, keys ...string) any {
	if len(keys) == 0 {
		return nil
	}
	current := m
	for _, key := range keys[:len(keys)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current[keys[len(keys)-1]]
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
