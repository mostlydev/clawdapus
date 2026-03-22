package main

import (
	"testing"
)

func TestComposePassthroughRegistered(t *testing.T) {
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "compose [subcommand] [args...]" {
			return
		}
	}
	t.Fatal("expected 'compose' command to be registered on rootCmd")
}
