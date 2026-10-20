package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runs"
	"github.com/spf13/cobra"
)

var (
	runsSocketDir      string
	runsAllowUIDs      []string
	runsCallbackOrigin string
	runsCallbackPath   string
	runsHeartbeat      time.Duration
	runsServerUID      int

	runsGeneration    uint64
	runsSpecFile      string
	runsAuthorityTTL  time.Duration
	runsCredentialFn  string
	runsWait          string
	runsRemoveOrphans bool
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Serve and control per-run workers (x-claw.lifecycle: per-run)",
}

var runsServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the per-run worker supervisor on a Unix socket",
	Long: `Serve the per-run worker supervisor for this pod.

The supervisor holds the pod's run lock for its whole lifetime, so claw up and
claw down refuse to run until it stops. On start it reconciles the run ledger
against labelled containers before accepting connections, and it refuses to
serve while labelled run workers exist that the ledger does not know.

Trust model: the socket authenticates peers by Unix UID. Every allowed UID
(and root) holds every template and installation grant of this pod.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		podDir, p, err := loadRunsPod()
		if err != nil {
			return err
		}
		lock, err := runs.AcquirePodLock(podDir)
		if errors.Is(err, runs.ErrPodLocked) {
			return fmt.Errorf("another `claw runs serve`, `claw up` or `claw down` holds this pod's run lock")
		}
		if err != nil {
			return err
		}
		defer lock.Release()
		names, err := runs.ListTemplates(podDir)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			return fmt.Errorf("no per-run templates compiled for this pod; declare x-claw.lifecycle: per-run and run claw up")
		}
		allowed, err := parseUIDs(runsAllowUIDs)
		if err != nil {
			return err
		}
		socketDir, err := runsSocketDirFor(podDir, true)
		if err != nil {
			return err
		}
		exec, err := runs.NewDockerExecutor()
		if err != nil {
			return err
		}
		defer exec.Close()
		logf := func(format string, args ...any) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) }
		exec.Logf = logf
		cfg := runs.Config{
			PodDir:            podDir,
			Executor:          exec,
			Revoker:           &runs.CllamaRevoker{PodDir: podDir},
			HeartbeatInterval: runsHeartbeat,
			Logf:              logf,
		}
		if runsCallbackOrigin != "" {
			notifier, err := runs.NewHTTPNotifier(runsCallbackOrigin, runsCallbackPath)
			if err != nil {
				return err
			}
			cfg.Notifier = notifier
		}
		sup, err := runs.NewSupervisor(cfg)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		orphans, err := sup.Orphans(ctx)
		if err != nil {
			return fmt.Errorf("reconcile: list run workers: %w", err)
		}
		if len(orphans) > 0 {
			return fmt.Errorf("%d labelled run worker(s) have no ledger record; refusing to serve (see `claw runs doctor`)", len(orphans))
		}
		// Restart reconciliation runs before the socket accepts connections.
		sup.AdvanceAll(ctx)
		advancing := make(chan struct{})
		go func() {
			defer close(advancing)
			sup.Run(ctx, 2*time.Second)
		}()
		fmt.Fprintf(cmd.OutOrStdout(), "[claw] runs supervisor serving %s (templates: %s)\n", filepath.Join(socketDir, runs.SocketName), strings.Join(names, ", "))
		srv := &runs.Server{Sup: sup, Pod: p.Name, AllowedUID: allowed, Logf: logf}
		serveErr := srv.Listen(ctx, socketDir)
		// Listen returns only after every handler finished. Stop advancement
		// and wait for it too before the deferred lock release: writer
		// authority is never released while a mutation can still run.
		stop()
		<-advancing
		return serveErr
	},
}

var runsLaunchCmd = &cobra.Command{
	Use:   "launch <installation> <run-id>",
	Short: "Launch a run from a spec file (operator and test helper)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := readSpecInput(cmd.InOrStdin(), runsSpecFile)
		if err != nil {
			return err
		}
		_, _, digest, err := runs.CanonicalSpec(raw)
		if err != nil {
			return err
		}
		body := map[string]any{
			"installation":         args[0],
			"run_id":               args[1],
			"generation":           runsGeneration,
			"spec_digest":          digest,
			"spec":                 json.RawMessage(raw),
			"authority_expires_at": time.Now().UTC().Add(runsAuthorityTTL),
		}
		if runsCredentialFn != "" {
			cred := strings.TrimSpace(os.Getenv(runsCredentialFn))
			if cred == "" {
				return fmt.Errorf("environment variable %s is empty", runsCredentialFn)
			}
			body["credential"] = cred
		}
		return runsRequest(cmd, "POST", "/v1/runs", body)
	},
}

var runsStatusCmd = &cobra.Command{
	Use:   "status <installation> <run-id>",
	Short: "Show a run's status",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := runs.RunPath(runs.Key{Installation: args[0], RunID: args[1]}, "")
		if runsWait != "" {
			path += "?wait=" + runsWait
		}
		return runsRequest(cmd, "GET", path, nil)
	},
}

var runsCancelCmd = &cobra.Command{
	Use:   "cancel <installation> <run-id>",
	Short: "Cancel a run (tombstone, revoke, remove)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := runs.RunPath(runs.Key{Installation: args[0], RunID: args[1]}, "/cancel")
		if runsWait != "" {
			path += "?wait=" + runsWait
		}
		return runsRequest(cmd, "POST", path, map[string]any{"generation": runsGeneration})
	},
}

var runsResultCmd = &cobra.Command{
	Use:   "result <installation> <run-id>",
	Short: "Print a finished run's answer",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := runsClient()
		if err != nil {
			return err
		}
		var raw []byte
		if err := client.Do(cmd.Context(), "GET", runs.RunPath(runs.Key{Installation: args[0], RunID: args[1]}, "/result"), nil, &raw); err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(raw)
		return err
	},
}

var runsDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the run ledger and list run workers it does not know",
	RunE: func(cmd *cobra.Command, args []string) error {
		podDir, _, err := loadRunsPod()
		if err != nil {
			return err
		}
		lock, err := runs.AcquirePodLock(podDir)
		if errors.Is(err, runs.ErrPodLocked) {
			return fmt.Errorf("`claw runs serve` (or claw up/down) holds this pod's run lock; serve already refuses to start with orphans")
		}
		if err != nil {
			return err
		}
		defer lock.Release()
		exec, err := runs.NewDockerExecutor()
		if err != nil {
			return err
		}
		defer exec.Close()
		sup, err := runs.NewSupervisor(runs.Config{PodDir: podDir, Executor: exec, Revoker: &runs.CllamaRevoker{PodDir: podDir}})
		if err != nil {
			return err
		}
		live, err := sup.LiveKeys()
		if err != nil {
			return fmt.Errorf("ledger unreadable: %w", err)
		}
		orphans, err := sup.Orphans(cmd.Context())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "ledger: ok (%d unfinished run(s))\n", len(live))
		for _, k := range live {
			fmt.Fprintf(out, "  unfinished: %s/%s\n", k.Installation, k.RunID)
		}
		if len(orphans) == 0 {
			fmt.Fprintln(out, "orphans: none")
			return nil
		}
		fmt.Fprintf(out, "orphans: %d labelled run worker(s) without a ledger record\n", len(orphans))
		for _, o := range orphans {
			fmt.Fprintf(out, "  container %s installation=%s run=%s key=%s\n",
				shortContainerIDForPostApply(o.ID), o.Labels[runs.LabelInstall], o.Labels[runs.LabelRunID], o.Labels[runs.LabelRunKey])
		}
		if !runsRemoveOrphans {
			fmt.Fprintln(out, "Inspect each with `docker inspect <container>`; once confirmed, remove them through Compose with `claw runs doctor --remove-orphans`.")
			return fmt.Errorf("orphaned run workers found")
		}
		for _, o := range orphans {
			if err := sup.RemoveOrphan(cmd.Context(), o); err != nil {
				return err
			}
			fmt.Fprintf(out, "  removed %s\n", shortContainerIDForPostApply(o.ID))
		}
		return nil
	},
}

func loadRunsPod() (string, *pod.Pod, error) {
	podFile := composePodFile
	if podFile == "" {
		podFile = "claw-pod.yml"
	}
	if _, err := resolveComposeGeneratedPath(); err != nil {
		return "", nil, err
	}
	p, podDir, err := loadPodDefinition(podFile)
	if err != nil {
		return "", nil, err
	}
	return podDir, p, nil
}

// runsSocketDirFor returns the configured socket directory, defaulting to
// <pod>/.claw-runs/sock (created 0700 when serving).
func runsSocketDirFor(podDir string, create bool) (string, error) {
	dir := runsSocketDir
	if dir == "" {
		dir = filepath.Join(podDir, runs.StateDirName, "sock")
		if create {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return "", err
			}
		}
	}
	return filepath.Abs(dir)
}

func runsClient() (*runs.Client, error) {
	dir := runsSocketDir
	if dir == "" {
		podFile := composePodFile
		if podFile == "" {
			podFile = "claw-pod.yml"
		}
		abs, err := filepath.Abs(podFile)
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(filepath.Dir(abs), runs.StateDirName, "sock")
	}
	uid := runsServerUID
	if uid < 0 {
		uid = os.Geteuid()
	}
	return runs.Dial(dir, uint32(uid))
}

func runsRequest(cmd *cobra.Command, method, path string, body any) error {
	client, err := runsClient()
	if err != nil {
		return err
	}
	var raw []byte
	err = client.Do(cmd.Context(), method, path, body, &raw)
	if err != nil {
		return err
	}
	_, err = cmd.OutOrStdout().Write(raw)
	return err
}

func readSpecInput(stdin io.Reader, path string) ([]byte, error) {
	if path == "" || path == "-" {
		return io.ReadAll(io.LimitReader(stdin, 64<<10))
	}
	return os.ReadFile(path)
}

func parseUIDs(values []string) ([]uint32, error) {
	out := make([]uint32, 0, len(values))
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("--allow-uid %q is not a numeric UID", part)
			}
			out = append(out, uint32(n))
		}
	}
	return out, nil
}

func init() {
	runsCmd.PersistentFlags().StringVar(&runsSocketDir, "socket-dir", "", "Directory holding claw-runs.sock (default <pod>/.claw-runs/sock; must be 0700 or 0750)")
	runsServeCmd.Flags().StringSliceVar(&runsAllowUIDs, "allow-uid", nil, "Additional UIDs allowed to connect (the supervisor's own UID is always allowed)")
	runsServeCmd.Flags().StringVar(&runsCallbackOrigin, "callback-origin", "", "Orchestrator origin for lifecycle callbacks (scheme://host[:port]); enables credential custody")
	runsServeCmd.Flags().StringVar(&runsCallbackPath, "callback-path", "/api/runs/{run_id}", "Callback path template; must contain {run_id}")
	runsServeCmd.Flags().DurationVar(&runsHeartbeat, "heartbeat", time.Minute, "Heartbeat callback interval")
	runsCmd.PersistentFlags().IntVar(&runsServerUID, "server-uid", -1, "Expected supervisor UID for client commands (default: your own)")
	for _, c := range []*cobra.Command{runsLaunchCmd, runsCancelCmd} {
		c.Flags().Uint64Var(&runsGeneration, "generation", 1, "Run generation fence")
	}
	runsLaunchCmd.Flags().StringVar(&runsSpecFile, "spec", "-", "Spec JSON file (- for stdin)")
	runsLaunchCmd.Flags().DurationVar(&runsAuthorityTTL, "authority-ttl", 30*time.Minute, "How long the launch's authority lasts")
	runsLaunchCmd.Flags().StringVar(&runsCredentialFn, "credential-env", "", "Name of an env var holding the orchestrator credential to place in custody")
	for _, c := range []*cobra.Command{runsStatusCmd, runsCancelCmd} {
		c.Flags().StringVar(&runsWait, "wait", "", "Long-poll until this state (running, revoked, stopped, failed)")
	}
	runsDoctorCmd.Flags().BoolVar(&runsRemoveOrphans, "remove-orphans", false, "Remove orphaned run workers of this pod through Compose")
	runsCmd.AddCommand(runsServeCmd, runsLaunchCmd, runsStatusCmd, runsCancelCmd, runsResultCmd, runsDoctorCmd)
	rootCmd.AddCommand(runsCmd)
}
