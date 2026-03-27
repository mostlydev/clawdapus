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
		maybeNotifyUpdate()
	},
}

func init() {
	rootCmd.Version = fmt.Sprintf("%s (%s)", version, commit)
}
