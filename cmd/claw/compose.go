package main

import "github.com/spf13/cobra"

var composeCmd = &cobra.Command{
	Use:   "compose",
	Short: "Pod lifecycle commands (up, down, ps, logs)",
}

func init() {
	rootCmd.AddCommand(composeCmd)
}
