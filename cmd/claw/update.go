package main

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update claw to the latest release",
	RunE: func(cmd *cobra.Command, args []string) error {
		sh, err := exec.LookPath("sh")
		if err != nil {
			return err
		}
		curl := exec.Command("curl", "-sSL",
			"https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh")
		install := exec.Command(sh)
		install.Stdin, err = curl.StdoutPipe()
		if err != nil {
			return err
		}
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := curl.Start(); err != nil {
			return err
		}
		if err := install.Start(); err != nil {
			return err
		}
		if err := curl.Wait(); err != nil {
			return err
		}
		return install.Wait()
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
