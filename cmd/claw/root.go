package main

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:          "claw",
	Short:        "Infrastructure-layer governance for AI agent containers",
	SilenceUsage: true,
}
