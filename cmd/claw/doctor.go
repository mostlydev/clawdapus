package main

import (
	"fmt"

	"github.com/mostlydev/clawdapus/internal/doctor"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Docker CLI, buildx, and compose availability",
	RunE: func(cmd *cobra.Command, args []string) error {
		results := doctor.RunAll()
		allOK := true

		for _, result := range results {
			status := "OK"
			if !result.OK {
				status = "FAIL"
				allOK = false
			}

			if result.Version != "" {
				fmt.Printf("%-10s %-4s %s\n", result.Name, status, result.Version)
			} else {
				fmt.Printf("%-10s %-4s %s\n", result.Name, status, result.Detail)
			}
		}

		if !allOK {
			return fmt.Errorf("one or more checks failed")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
