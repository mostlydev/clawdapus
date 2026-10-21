package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/schedule"
)

func writeScheduleManifest(runtimeDir string, p *pod.Pod, resolved map[string]*driver.ResolvedClaw) (string, error) {
	manifest, err := buildScheduleManifest(p, resolved)
	if err != nil {
		return "", fmt.Errorf("build schedule manifest: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode schedule manifest: %w", err)
	}

	path := filepath.Join(runtimeDir, "schedule.json")
	if err := writeRuntimeFile(path, append(data, '\n'), 0644); err != nil {
		return "", fmt.Errorf("write schedule manifest %q: %w", path, err)
	}
	return path, nil
}

func buildScheduleManifest(p *pod.Pod, resolved map[string]*driver.ResolvedClaw) (*schedule.Manifest, error) {
	manifest := &schedule.Manifest{
		Version: 1,
	}
	if p != nil {
		manifest.Pod = p.Name
	}

	names := make([]string, 0, len(resolved))
	for name := range resolved {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := resolved[name]
		if rc == nil {
			continue
		}
		serviceTimezone := strings.TrimSpace(rc.Timezone)
		if serviceTimezone == "" {
			serviceTimezone = "UTC"
		}
		for _, inv := range rc.Invocations {
			_, direct := directTurnWakerFor(rc)
			if inv.Origin != driver.OriginPod && !direct {
				continue
			}
			if !supportsExternalScheduler(rc.ClawType) && !direct {
				if inv.When != nil {
					fmt.Printf("[claw] warning: service %q: driver %q does not support external scheduler; ignoring calendar gating for %q\n", name, rc.ClawType, invocationDisplayName(inv))
				}
				continue
			}

			timezone := serviceTimezone
			if inv.When != nil {
				cal, err := schedule.LookupCalendar(inv.When.Calendar)
				if err != nil {
					return nil, fmt.Errorf("service %q invocation %q: %w", name, invocationDisplayName(inv), err)
				}
				timezone = cal.Timezone
			}

			count := rc.Count
			if count < 1 {
				count = 1
			}
			if warning := scheduleWakeWarning(name, rc.ClawType, inv); warning != "" {
				fmt.Printf("[claw] warning: %s\n", warning)
			}
			for _, agentID := range expandedServiceNames(name, count) {
				wake, err := resolveWakeAdapter(rc, agentID, inv)
				if err != nil {
					return nil, fmt.Errorf("service %q invocation %q: %w", name, invocationDisplayName(inv), err)
				}
				timeoutSeconds := 0
				if waker, direct := directTurnWakerFor(rc); direct {
					timeoutSeconds, err = waker.InvocationTimeoutSeconds(rc)
					if err != nil {
						return nil, err
					}
				}
				manifest.Invocations = append(manifest.Invocations, schedule.ManifestInvocation{
					TimeoutSeconds: timeoutSeconds,
					ID:             scheduleManifestID(agentID, inv),
					Service:        name,
					AgentID:        agentID,
					Schedule:       inv.Schedule,
					Timezone:       timezone,
					Message:        inv.Message,
					Name:           inv.Name,
					To:             inv.To,
					When:           inv.When.Clone(),
					Wake:           wake,
				})
			}
		}
	}

	return manifest, nil
}

func supportsExternalScheduler(clawType string) bool {
	switch strings.TrimSpace(clawType) {
	case "openclaw", "hermes", "nanobot", "picoclaw":
		return true
	default:
		return false
	}
}

func resolveWakeAdapter(rc *driver.ResolvedClaw, target string, inv driver.Invocation) (schedule.Wake, error) {
	if waker, direct := directTurnWakerFor(rc); direct {
		seconds, err := waker.InvocationTimeoutSeconds(rc)
		if err != nil {
			return schedule.Wake{}, err
		}
		command, err := directTurnWakeCommand(waker.WakeHelper(), rc, inv, seconds)
		if err != nil {
			return schedule.Wake{}, err
		}
		return schedule.Wake{Adapter: rc.ClawType + "-exec", Target: target, Command: command}, nil
	}
	clawType := rc.ClawType
	nativeID := strings.TrimSpace(inv.ID)
	switch strings.TrimSpace(clawType) {
	case "hermes":
		if nativeID == "" {
			return schedule.Wake{}, fmt.Errorf("missing native invocation id")
		}
		return schedule.Wake{
			Adapter: "hermes-exec",
			Target:  target,
			Command: []string{"hermes", "cron", "run", nativeID},
		}, nil
	case "nanobot":
		if nativeID == "" {
			return schedule.Wake{}, fmt.Errorf("missing native invocation id")
		}
		return schedule.Wake{
			Adapter: "nanobot-exec",
			Target:  target,
			Command: []string{"nanobot", "cron", "run", nativeID},
		}, nil
	case "picoclaw":
		return schedule.Wake{
			Adapter: "picoclaw-exec",
			Target:  target,
			Command: []string{"picoclaw", "agent", "-m", inv.Message},
		}, nil
	default:
		return schedule.Wake{}, fmt.Errorf("unsupported wake adapter for driver %q", clawType)
	}
}

func scheduleWakeWarning(serviceName, clawType string, inv driver.Invocation) string {
	if strings.TrimSpace(inv.To) == "" {
		return ""
	}
	switch strings.TrimSpace(clawType) {
	case "picoclaw":
		return fmt.Sprintf("service %q: driver %q external scheduler wake does not support invoke.to=%q; firing direct agent message without delivery routing", serviceName, clawType, inv.To)
	default:
		return ""
	}
}

func scheduleManifestID(agentID string, inv driver.Invocation) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(agentID),
		string(inv.Origin),
		strings.TrimSpace(inv.Schedule),
		strings.TrimSpace(inv.Message),
	}, "|")))
	return hex.EncodeToString(sum[:6])
}

func invocationDisplayName(inv driver.Invocation) string {
	if strings.TrimSpace(inv.Name) != "" {
		return strings.TrimSpace(inv.Name)
	}
	return strings.TrimSpace(inv.Message)
}

// The baked helper accepts only positional data; task text is never evaluated
// as shell syntax. Empty target deliberately requests no automatic delivery.
func directTurnWakeCommand(helper string, rc *driver.ResolvedClaw, inv driver.Invocation, seconds int) ([]string, error) {
	if seconds < 1 || seconds > schedule.MaxInvocationTimeoutSeconds {
		return nil, fmt.Errorf("scheduled turn timeout must be between 1 and %d", schedule.MaxInvocationTimeoutSeconds)
	}
	channel, target := inv.Channel, strings.TrimSpace(inv.To)
	if target != "" && channel == "" {
		platforms := map[string]bool{}
		for _, m := range findInvokeChannelMatches(rc.Handles, "", target, true) {
			platforms[m.Platform] = true
		}
		unmatched := len(platforms) == 0
		if unmatched {
			for platform := range rc.Handles {
				platforms[platform] = true
			}
		}
		if len(platforms) != 1 {
			return nil, fmt.Errorf("scheduled delivery target requires an unambiguous platform; use platform:target")
		}
		for p := range platforms {
			channel = p
		}
		if unmatched && channel == "discord" {
			if _, err := strconv.ParseUint(strings.TrimPrefix(target, "channel:"), 10, 64); err != nil {
				return nil, fmt.Errorf("unknown Discord scheduled delivery target; use a declared channel name or numeric ID")
			}
		}
	}
	if target != "" {
		if channel == "discord" && len(findInvokeChannelMatches(rc.Handles, channel, target, true)) == 0 {
			if _, err := strconv.ParseUint(strings.TrimPrefix(target, "channel:"), 10, 64); err != nil {
				return nil, fmt.Errorf("unknown Discord scheduled delivery target; use a declared channel name or numeric ID")
			}
		}
		if _, ok := rc.Handles[channel]; !ok {
			return nil, fmt.Errorf("scheduled delivery platform %q is not declared", channel)
		}
	}
	return []string{helper, inv.ID, inv.Message, strconv.Itoa(seconds), channel, target}, nil
}

func directTurnWakerFor(rc *driver.ResolvedClaw) (driver.DirectTurnWaker, bool) {
	if rc == nil {
		return nil, false
	}
	d, err := driver.Lookup(rc.ClawType)
	if err != nil {
		return nil, false
	}
	w, ok := d.(driver.DirectTurnWaker)
	return w, ok
}
