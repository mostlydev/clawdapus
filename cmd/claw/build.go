package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/spf13/cobra"
)

var buildTag string
var buildContext string

var buildCmd = &cobra.Command{
	Use:   "build [path-or-clawfile]",
	Short: "Compile a Clawfile to Dockerfile and build the image",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if podFile, ok, err := resolveOptionalPodFile(composePodFile, nil); err != nil {
				return err
			} else if ok {
				if buildTag != "" || strings.TrimSpace(buildContext) != "" {
					return fmt.Errorf("pod-aware 'claw build' does not accept --tag or --context")
				}
				return runBuildPod(podFile)
			}
		}

		input := "."
		if len(args) == 1 {
			input = args[0]
		}

		clawfilePath, err := resolveClawfilePath(input)
		if err != nil {
			return err
		}
		contextDir, err := resolveBuildContext(buildContext, clawfilePath)
		if err != nil {
			return err
		}

		fmt.Printf("Generating Dockerfile from %s\n", clawfilePath)
		generatedPath, err := build.Generate(clawfilePath)
		if err != nil {
			return formatGenerateError(err, "", clawfilePath)
		}
		fmt.Printf("Generated %s\n", generatedPath)

		fmt.Println("Building image with docker")
		return build.BuildFromGenerated(generatedPath, buildTag, contextDir)
	},
}

func runBuildPod(podFile string) error {
	p, podDir, err := loadPodDefinition(podFile)
	if err != nil {
		return err
	}

	plans, err := planPodServiceImages(p)
	if err != nil {
		return err
	}

	buildCount := 0
	for _, plan := range plans {
		if plan.BuildConfig != nil {
			buildCount++
		}
	}
	if buildCount == 0 {
		fmt.Println("[claw] no pod services declare build:")
		return nil
	}

	if err := buildPlannedServiceImages(podFile, podDir, plans, false); err != nil {
		return err
	}

	fmt.Printf("[claw] built %d pod service image(s)\n", buildCount)
	return nil
}

func resolveClawfilePath(input string) (string, error) {
	info, err := os.Stat(input)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("input path %q does not exist", input)
		}
		return "", fmt.Errorf("stat %s: %w", input, err)
	}

	if info.IsDir() {
		clawfilePath := filepath.Join(input, "Clawfile")
		if _, err := os.Stat(clawfilePath); err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("no Clawfile found in directory %q", input)
			}
			return "", fmt.Errorf("stat %s: %w", clawfilePath, err)
		}
		return clawfilePath, nil
	}

	return input, nil
}

func resolveBuildContext(input, clawfilePath string) (string, error) {
	contextDir := strings.TrimSpace(input)
	if contextDir == "" {
		contextDir = filepath.Dir(clawfilePath)
	}

	resolved, err := filepath.Abs(contextDir)
	if err != nil {
		return "", fmt.Errorf("resolve build context %q: %w", contextDir, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("build context %q does not exist", contextDir)
		}
		return "", fmt.Errorf("stat build context %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("build context %q is not a directory", contextDir)
	}

	return resolved, nil
}

func pullCommandForPod(podFile string) string {
	return "claw pull -f " + strings.TrimSpace(podFile)
}

func pullCommandForClawfile(clawfilePath string) string {
	return "claw pull " + strings.TrimSpace(clawfilePath)
}

func formatGenerateError(err error, podFile, clawfilePath string) error {
	var missing *build.MissingRunnerBaseError
	if errors.As(err, &missing) {
		switch {
		case strings.TrimSpace(podFile) != "":
			return remediationErrorf(pullCommandForPod(podFile), "%s", missing.Error())
		case strings.TrimSpace(clawfilePath) != "":
			return remediationErrorf(pullCommandForClawfile(clawfilePath), "%s", missing.Error())
		default:
			return err
		}
	}

	var refresh *build.RunnerRefreshRequiredError
	if errors.As(err, &refresh) {
		switch {
		case strings.TrimSpace(podFile) != "":
			return remediationErrorf(pullCommandForPod(podFile), "%s", refresh.Error())
		case strings.TrimSpace(clawfilePath) != "":
			return remediationErrorf(pullCommandForClawfile(clawfilePath), "%s", refresh.Error())
		default:
			return err
		}
	}

	return err
}

func init() {
	buildCmd.Flags().StringVarP(&buildTag, "tag", "t", "", "Tag for the built image")
	buildCmd.Flags().StringVar(&buildContext, "context", "", "Docker build context directory (defaults to the Clawfile directory)")
	rootCmd.AddCommand(buildCmd)
}
