package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var pullCmd = &cobra.Command{
	Use:   "pull [pod-file]",
	Short: "Fetch pinned infra images and pod registry images",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		podFile, ok, err := resolveOptionalPodFile(composePodFile, args)
		if err != nil {
			return err
		}
		return runPull(podFile, ok)
	},
}

func runPull(podFile string, hasPod bool) error {
	if !hasPod {
		if err := pullCoreInfraImages(); err != nil {
			return err
		}
		fmt.Println("[claw] infra images are ready")
		return nil
	}

	p, podDir, err := loadPodDefinition(podFile)
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

	fmt.Println("[claw] pull complete")
	return nil
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
