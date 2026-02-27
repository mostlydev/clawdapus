package main

import (
	"fmt"
	"sort"
	"strings"

	clawinspect "github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <image>",
	Short: "Show parsed Claw metadata from image labels",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := clawinspect.Inspect(args[0])
		if err != nil {
			return err
		}

		if info.ClawType == "" {
			fmt.Println("Not a Claw image (no claw.type label)")
			return nil
		}

		fmt.Printf("Claw Type: %s\n", info.ClawType)
		fmt.Printf("Agent:     %s\n", info.Agent)
		if len(info.Cllama) > 0 {
			fmt.Printf("Cllama:    %s\n", strings.Join(info.Cllama, ", "))
		}
		if info.Persona != "" {
			fmt.Printf("Persona:   %s\n", info.Persona)
		}

		modelSlots := sortedKeys(info.Models)
		for _, slot := range modelSlots {
			fmt.Printf("Model[%s]: %s\n", slot, info.Models[slot])
		}

		for _, surface := range info.Surfaces {
			fmt.Printf("Surface:   %s\n", surface)
		}

		privModes := sortedKeys(info.Privileges)
		for _, mode := range privModes {
			fmt.Printf("Privilege[%s]: %s\n", mode, info.Privileges[mode])
		}

		return nil
	},
}

func sortedKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
