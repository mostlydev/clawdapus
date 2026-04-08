package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

var rootCmd = &cobra.Command{
	Use:          "claw",
	Short:        "Infrastructure-layer governance for AI agent containers",
	SilenceUsage: true,
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		maybeSyncSkill()
		// Skip the "update available" notice for `claw update` itself —
		// the running process is still the old binary, so the notice would
		// fire immediately after a successful update and tell the user to
		// run the command they just ran.
		if cmd.Name() != "update" {
			maybeNotifyUpdate()
		}
	},
}

func init() {
	rootCmd.Version = fmt.Sprintf("%s (%s)", version, commit)
}
