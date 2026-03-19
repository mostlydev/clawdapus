package main

import (
	"context"
	"encoding/json"
	"fmt"
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
		manifest, err := loadRuntimeManifest()
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
		filtered := audit.FilterEvents(events, audit.Filter{
			ClawID: auditClaw,
			Type:   auditType,
			Since:  since,
		})
		summary := audit.Summarize(filtered)
		if auditJSON {
			payload := map[string]any{
				"pod":           manifest.PodName,
				"skipped_lines": skipped,
				"summary":       summary,
				"events":        filtered,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		fmt.Printf("Pod: %s\n", manifest.PodName)
		fmt.Printf("Events: %d", len(filtered))
		if skipped > 0 {
			fmt.Printf(" (skipped malformed lines: %d)", skipped)
		}
		fmt.Println()
		if len(summary.Agents) == 0 {
			fmt.Println("No telemetry events matched the current filters.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "CLAW\tREQ\tRESP\tERR\tINT\tTOK_IN\tTOK_OUT\tCOST_USD\tMODELS")
		for _, item := range summary.Agents {
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%.4f\t%s\n",
				item.ClawID,
				item.Requests,
				item.Responses,
				item.Errors,
				item.Interventions,
				item.TokensIn,
				item.TokensOut,
				item.CostUSD,
				formatModelUsage(item.ModelUsage),
			)
		}
		w.Flush()

		fmt.Printf("\nTotals: req=%d resp=%d err=%d int=%d tokens=%d/%d cost=$%.4f\n",
			summary.Requests,
			summary.Responses,
			summary.Errors,
			summary.Interventions,
			summary.TokensIn,
			summary.TokensOut,
			summary.CostUSD,
		)
		return nil
	},
}

func loadRuntimeManifest() (*clawdash.PodManifest, error) {
	generatedPath, err := resolveComposeGeneratedPath()
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(filepath.Dir(generatedPath), ".claw-runtime", "pod-manifest.json")
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
	auditCmd.Flags().StringVar(&auditType, "type", "", "Only include one event type (request, response, error, intervention)")
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "Emit machine-readable JSON")
	rootCmd.AddCommand(auditCmd)
}
