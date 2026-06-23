package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/docker/client"
	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/audit"
	"github.com/mostlydev/clawdapus/internal/clawdash"
)

var (
	auditSince string
	auditClaw  string
	auditType  string
	auditJSON  bool
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Summarize normalized cllama telemetry for the current pod",
	RunE: func(cmd *cobra.Command, args []string) error {
		podDir, err := resolveCurrentPodDir()
		if err != nil {
			return err
		}
		manifest, err := loadRuntimeManifest(podDir)
		if err != nil {
			return err
		}
		since, err := parseSinceArg(auditSince)
		if err != nil {
			return err
		}

		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			return fmt.Errorf("docker client: %w", err)
		}
		defer cli.Close()

		events, skipped, err := audit.CollectPodEvents(context.Background(), cli, manifest.PodName, since)
		if err != nil {
			return err
		}
		historyEvents, historySkipped, err := audit.CollectSessionHistoryEvents(filepath.Join(podDir, ".claw-session-history"), since)
		if err != nil {
			return err
		}
		events = append(events, historyEvents...)
		skipped += historySkipped
		filtered := audit.FilterEvents(events, audit.Filter{
			ClawID: auditClaw,
			Type:   auditType,
			Since:  since,
		})
		summary := audit.Summarize(filtered)
		out := cmd.OutOrStdout()
		if auditJSON {
			return writeAuditJSON(out, manifest.PodName, skipped, summary, filtered)
		}
		return writeAuditText(out, manifest.PodName, skipped, summary, filtered)
	},
}

func resolveCurrentPodDir() (string, error) {
	generatedPath, err := resolveComposeGeneratedPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(generatedPath), nil
}

func loadRuntimeManifest(podDir string) (*clawdash.PodManifest, error) {
	manifestPath := filepath.Join(podDir, ".claw-runtime", "pod-manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read pod manifest %q: %w", manifestPath, err)
	}
	var manifest clawdash.PodManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse pod manifest %q: %w", manifestPath, err)
	}
	if strings.TrimSpace(manifest.PodName) == "" {
		return nil, fmt.Errorf("pod manifest %q is missing podName", manifestPath)
	}
	return &manifest, nil
}

func writeAuditJSON(w io.Writer, podName string, skipped int, summary audit.Summary, events []audit.Event) error {
	payload := map[string]any{
		"pod":           podName,
		"skipped_lines": skipped,
		"summary":       summary,
		"events":        events,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func writeAuditText(w io.Writer, podName string, skipped int, summary audit.Summary, events []audit.Event) error {
	fmt.Fprintf(w, "Pod: %s\n", podName)
	fmt.Fprintf(w, "Events: %d", len(events))
	if skipped > 0 {
		fmt.Fprintf(w, " (skipped malformed lines: %d)", skipped)
	}
	fmt.Fprintln(w)
	if len(summary.Agents) == 0 {
		_, err := fmt.Fprintln(w, "No telemetry events matched the current filters.")
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CLAW\tREQ\tRESP\tERR\tINT\tTOOLS\tTOOL_ERR\tTOK_IN\tTOK_OUT\tCOST_USD\tMODELS")
	for _, item := range summary.Agents {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%.4f\t%s\n",
			item.ClawID,
			item.Requests,
			item.Responses,
			item.Errors,
			item.Interventions,
			item.ToolCalls,
			item.ToolErrors,
			item.TokensIn,
			item.TokensOut,
			item.CostUSD,
			formatModelUsage(item.ModelUsage),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	_, err := fmt.Fprintf(w, "\nTotals: req=%d resp=%d err=%d int=%d tools=%d/%d tokens=%d/%d cost=$%.4f\n",
		summary.Requests,
		summary.Responses,
		summary.Errors,
		summary.Interventions,
		summary.ToolCalls,
		summary.ToolErrors,
		summary.TokensIn,
		summary.TokensOut,
		summary.CostUSD,
	)
	return err
}

func parseSinceArg(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, nil
	}
	if dur, err := time.ParseDuration(value); err == nil {
		return time.Now().Add(-dur), nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since value %q: use duration (e.g. 1h) or RFC3339", raw)
}

func formatModelUsage(models map[string]int) string {
	if len(models) == 0 {
		return "-"
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s(%d)", name, models[name]))
	}
	return strings.Join(parts, ", ")
}

func init() {
	auditCmd.Flags().StringVar(&auditSince, "since", "", "Only include events since this duration or RFC3339 timestamp")
	auditCmd.Flags().StringVar(&auditClaw, "claw", "", "Only include events for one claw_id")
	auditCmd.Flags().StringVar(&auditType, "type", "", "Only include one event type (for example request, response, error, intervention, feed_fetch, channel_context_op, provider_pool, tool_call)")
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "Emit machine-readable JSON")
	rootCmd.AddCommand(auditCmd)
}
