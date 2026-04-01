package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultHistoryExportLimit = 100
	maxHistoryExportLimit     = 1000
)

var (
	historyAfter string
	historyLimit int
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Inspect persistent agent session history",
}

var historyExportCmd = &cobra.Command{
	Use:   "export <agent-id>",
	Short: "Export an agent's session history as NDJSON",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		after, err := parseHistoryAfter(historyAfter)
		if err != nil {
			return err
		}
		limit, err := normalizeHistoryLimit(historyLimit)
		if err != nil {
			return err
		}
		podDir, err := resolveHistoryPodDir()
		if err != nil {
			return err
		}
		historyPath := filepath.Join(podDir, ".claw-session-history", args[0], "history.jsonl")
		return exportHistoryFile(cmd.OutOrStdout(), historyPath, after, limit)
	},
}

func resolveHistoryPodDir() (string, error) {
	if composePodFile != "" {
		absPodFile, err := filepath.Abs(composePodFile)
		if err != nil {
			return "", fmt.Errorf("resolve pod file path %q: %w", composePodFile, err)
		}
		if _, err := os.Stat(absPodFile); err != nil {
			return "", fmt.Errorf("open pod file %q: %w", composePodFile, err)
		}
		return filepath.Dir(absPodFile), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	return cwd, nil
}

func parseHistoryAfter(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("after must be RFC3339")
	}
	return &ts, nil
}

func normalizeHistoryLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("limit must be > 0")
	}
	if limit > maxHistoryExportLimit {
		return maxHistoryExportLimit, nil
	}
	return limit, nil
}

func exportHistoryFile(w io.Writer, historyPath string, after *time.Time, limit int) error {
	f, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no session history found at %q", historyPath)
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	emitted := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		lineWithID, meta, err := ensureHistoryEntryID(line)
		if err != nil {
			return err
		}
		if after != nil {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(meta.TS))
			if err != nil {
				return fmt.Errorf("parse history timestamp %q: %w", meta.TS, err)
			}
			if !ts.After(*after) {
				continue
			}
		}
		if _, err := fmt.Fprintln(w, string(lineWithID)); err != nil {
			return err
		}
		emitted++
		if emitted >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func init() {
	historyExportCmd.Flags().StringVar(&historyAfter, "after", "", "Only emit entries after this RFC3339 timestamp")
	historyExportCmd.Flags().IntVar(&historyLimit, "limit", defaultHistoryExportLimit, "Maximum number of entries to emit")
	historyCmd.AddCommand(historyExportCmd)
	rootCmd.AddCommand(historyCmd)
}
