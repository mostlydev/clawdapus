package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/spf13/cobra"
)

var buildTag string

var buildCmd = &cobra.Command{
	Use:   "build [path-or-clawfile]",
	Short: "Compile a Clawfile to Dockerfile and build the image",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		input := "."
		if len(args) == 1 {
			input = args[0]
		}

		clawfilePath, err := resolveClawfilePath(input)
		if err != nil {
			return err
		}

		fmt.Printf("Generating Dockerfile from %s\n", clawfilePath)
		generatedPath, err := build.Generate(clawfilePath)
		if err != nil {
			return err
		}
		fmt.Printf("Generated %s\n", generatedPath)

		fmt.Println("Building image with docker")
		return build.BuildFromGenerated(generatedPath, buildTag)
	},
}

func resolveClawfilePath(input string) (string, error) {
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Join(input, "Clawfile"), nil
		}
		return "", fmt.Errorf("stat %s: %w", input, err)
	}

	if info.IsDir() {
		return filepath.Join(input, "Clawfile"), nil
	}

	return input, nil
}

func init() {
	buildCmd.Flags().StringVarP(&buildTag, "tag", "t", "", "Tag for the built image")
	rootCmd.AddCommand(buildCmd)
}
