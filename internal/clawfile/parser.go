package clawfile

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

var knownDirectives = map[string]bool{
	"claw_type": true,
	"agent":     true,
	"model":     true,
	"cllama":    true,
	"persona":   true,
	"surface":   true,
	"invoke":    true,
	"privilege": true,
	"configure": true,
	"track":     true,
	"act":       true,
}

type ParseResult struct {
	Config      *ClawConfig
	DockerNodes []*parser.Node
}

func Parse(r io.Reader) (*ParseResult, error) {
	parsed, err := parser.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse clawfile: %w", err)
	}

	config := NewClawConfig()
	dockerNodes := make([]*parser.Node, 0, len(parsed.AST.Children))

	for _, node := range parsed.AST.Children {
		command := strings.ToLower(strings.TrimSpace(node.Value))
		if command == "" {
			continue
		}

		if !knownDirectives[command] {
			if strings.HasPrefix(command, "claw_") {
				return nil, fmt.Errorf("line %d: unknown Claw directive %s", node.StartLine, strings.ToUpper(command))
			}
			dockerNodes = append(dockerNodes, node)
			continue
		}

		remainder := directiveRemainder(node)
		args := strings.Fields(remainder)
		switch command {
		case "claw_type":
			if len(args) < 1 {
				return nil, fmt.Errorf("line %d: CLAW_TYPE requires an argument", node.StartLine)
			}
			if err := setSingleton("CLAW_TYPE", &config.ClawType, args[0], node.StartLine); err != nil {
				return nil, err
			}

		case "agent":
			if len(args) < 1 {
				return nil, fmt.Errorf("line %d: AGENT requires a filename", node.StartLine)
			}
			if err := setSingleton("AGENT", &config.Agent, args[0], node.StartLine); err != nil {
				return nil, err
			}

		case "model":
			if len(args) < 2 {
				return nil, fmt.Errorf("line %d: MODEL requires <slot> <provider/model>", node.StartLine)
			}
			slot := args[0]
			if _, exists := config.Models[slot]; exists {
				return nil, fmt.Errorf("line %d: duplicate MODEL slot %q", node.StartLine, slot)
			}
			config.Models[slot] = strings.TrimSpace(strings.TrimPrefix(remainder, slot))

		case "cllama":
			if len(args) < 1 {
				return nil, fmt.Errorf("line %d: CLLAMA requires a value", node.StartLine)
			}
			if err := setSingleton("CLLAMA", &config.Cllama, remainder, node.StartLine); err != nil {
				return nil, err
			}

		case "persona":
			if len(args) < 1 {
				return nil, fmt.Errorf("line %d: PERSONA requires a value", node.StartLine)
			}
			if err := setSingleton("PERSONA", &config.Persona, args[0], node.StartLine); err != nil {
				return nil, err
			}

		case "surface":
			surface, err := parseSurface(args)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", node.StartLine, err)
			}
			config.Surfaces = append(config.Surfaces, surface)

		case "invoke":
			if len(args) < 6 {
				return nil, fmt.Errorf("line %d: INVOKE requires 5 cron fields + command", node.StartLine)
			}
			config.Invocations = append(config.Invocations, Invocation{
				Schedule: strings.Join(args[:5], " "),
				Command:  strings.TrimSpace(strings.TrimPrefix(remainder, strings.Join(args[:5], " "))),
			})

		case "privilege":
			if len(args) < 2 {
				return nil, fmt.Errorf("line %d: PRIVILEGE requires <mode> <user-spec>", node.StartLine)
			}
			mode := args[0]
			config.Privileges[mode] = strings.TrimSpace(strings.TrimPrefix(remainder, mode))

		case "configure":
			if strings.TrimSpace(remainder) == "" {
				return nil, fmt.Errorf("line %d: CONFIGURE requires a command", node.StartLine)
			}
			config.Configures = append(config.Configures, remainder)

		case "track":
			if len(args) == 0 {
				return nil, fmt.Errorf("line %d: TRACK requires at least one value", node.StartLine)
			}
			config.Tracks = append(config.Tracks, args...)

		case "act":
			// Included for forward compatibility with worker mode semantics.
		}
	}

	return &ParseResult{Config: config, DockerNodes: dockerNodes}, nil
}

func directiveRemainder(node *parser.Node) string {
	original := strings.TrimSpace(node.Original)
	if original == "" {
		return ""
	}

	command := strings.TrimSpace(node.Value)
	if command == "" {
		return original
	}

	if len(original) >= len(command) && strings.EqualFold(original[:len(command)], command) {
		return strings.TrimSpace(original[len(command):])
	}

	parts := strings.Fields(original)
	if len(parts) <= 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(parts[1:], " "))
}

func setSingleton(name string, target *string, value string, line int) error {
	if *target != "" {
		return fmt.Errorf("line %d: duplicate %s directive", line, name)
	}
	*target = value
	return nil
}

func parseSurface(args []string) (Surface, error) {
	if len(args) == 0 {
		return Surface{}, fmt.Errorf("SURFACE requires a URI")
	}

	raw := strings.Join(args, " ")
	parsed, err := url.Parse(args[0])
	if err != nil {
		return Surface{}, fmt.Errorf("invalid SURFACE URI %q: %w", args[0], err)
	}
	if parsed.Scheme == "" {
		return Surface{}, fmt.Errorf("SURFACE URI %q is missing scheme", args[0])
	}

	target := parsed.Host
	switch {
	case parsed.Host != "" && parsed.Path != "":
		target = parsed.Host + parsed.Path
	case parsed.Host == "" && parsed.Path != "":
		target = parsed.Path
	case parsed.Host == "" && parsed.Path == "" && parsed.Opaque != "":
		target = parsed.Opaque
	}
	if target == "" {
		return Surface{}, fmt.Errorf("SURFACE URI %q is missing target", args[0])
	}

	accessMode := ""
	if len(args) > 1 {
		accessMode = strings.Join(args[1:], " ")
	}

	return Surface{
		Raw:        raw,
		Scheme:     parsed.Scheme,
		Target:     target,
		AccessMode: accessMode,
	}, nil
}
