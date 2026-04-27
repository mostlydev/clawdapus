package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/spf13/cobra"
)

var pullNoRunners bool

var pullCmd = &cobra.Command{
	Use:   "pull [pod-file-or-clawfile]",
	Short: "Fetch pinned infra, pod registry images, and runner bases",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := resolvePullTarget(composePodFile, args)
		if err != nil {
			return err
		}
		return runPullTarget(target, pullOptions{NoRunners: pullNoRunners})
	},
}

type pullOptions struct {
	NoRunners bool
}

type pullTarget struct {
	PodFile      string
	HasPod       bool
	ClawfilePath string
	HasClawfile  bool
}

func resolvePullTarget(explicitPodFile string, args []string) (pullTarget, error) {
	if strings.TrimSpace(explicitPodFile) != "" && len(args) > 0 {
		return pullTarget{}, fmt.Errorf("pod file specified twice: use either '--file %s' or positional arg '%s', not both", explicitPodFile, args[0])
	}
	if strings.TrimSpace(explicitPodFile) != "" {
		return pullTarget{PodFile: explicitPodFile, HasPod: true}, nil
	}
	if len(args) > 0 {
		input := strings.TrimSpace(args[0])
		if pullArgLooksPodFile(input) {
			return pullTarget{PodFile: input, HasPod: true}, nil
		}
		clawfilePath, err := resolveClawfilePath(input)
		if err != nil {
			return pullTarget{}, err
		}
		return pullTarget{ClawfilePath: clawfilePath, HasClawfile: true}, nil
	}

	podFile, ok, err := resolveOptionalPodFile("", nil)
	if err != nil {
		return pullTarget{}, err
	}
	if ok {
		return pullTarget{PodFile: podFile, HasPod: true}, nil
	}
	return pullTarget{}, nil
}

func pullArgLooksPodFile(input string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(input)))
	return ext == ".yml" || ext == ".yaml"
}

func runPull(podFile string, hasPod bool) error {
	return runPullTarget(pullTarget{PodFile: podFile, HasPod: hasPod}, pullOptions{})
}

func runPullTarget(target pullTarget, opts pullOptions) error {
	if target.HasClawfile {
		if opts.NoRunners {
			fmt.Println("[claw] runner base refresh skipped (--no-runners)")
			return nil
		}
		name, provider, err := refreshableRunnerDriverForClawfile(target.ClawfilePath)
		if err != nil {
			return err
		}
		if err := refreshSelectedRunnerDrivers(map[string]driver.RunnerBaseProvider{name: provider}); err != nil {
			return err
		}
		fmt.Println("[claw] pull complete")
		return nil
	}

	if !target.HasPod {
		if err := pullCoreInfraImages(); err != nil {
			return err
		}
		if !opts.NoRunners {
			drivers := locallyTaggedRunnerDrivers()
			if len(drivers) == 0 {
				fmt.Println("[claw] no managed runner aliases tagged locally")
			} else if err := refreshSelectedRunnerDrivers(drivers); err != nil {
				return err
			}
		} else {
			fmt.Println("[claw] runner base refresh skipped (--no-runners)")
		}
		fmt.Println("[claw] pull complete")
		return nil
	}

	p, podDir, err := loadPodDefinition(target.PodFile)
	if err != nil {
		return err
	}

	plans, err := planPodServiceImages(p)
	if err != nil {
		return err
	}
	requiredInfra, err := requiredPodPullInfraSpecs(podDir, p, plans)
	if err != nil {
		return err
	}
	if err := pullInfraImagesFromRegistry(requiredInfra); err != nil {
		return err
	}
	if err := pullRegistryServiceImages(plans, false); err != nil {
		return err
	}
	if !opts.NoRunners {
		drivers, err := requiredRunnerDriversForPod(podDir, p, plans)
		if err != nil {
			return err
		}
		if len(drivers) == 0 {
			fmt.Println("[claw] no refreshable local runner aliases selected")
		} else if err := refreshSelectedRunnerDrivers(drivers); err != nil {
			return err
		}
	} else {
		fmt.Println("[claw] runner base refresh skipped (--no-runners)")
	}

	fmt.Println("[claw] pull complete")
	return nil
}

func init() {
	pullCmd.Flags().BoolVar(&pullNoRunners, "no-runners", false, "Skip local runner base refresh")
	rootCmd.AddCommand(pullCmd)
}
