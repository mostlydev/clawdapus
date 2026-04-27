package infraimages

import "fmt"

const (
	DefaultClawInfraTag  = "v0.10.0"
	DefaultClawAPITag    = DefaultClawInfraTag
	DefaultClawdashTag   = DefaultClawInfraTag
	DefaultClawWallTag   = DefaultClawInfraTag
	DefaultCllamaTag     = "v0.4.1"
	DefaultHermesBaseTag = "v2026.3.17"
)

func ReleaseRefs(releaseTag string) []string {
	return []string{
		fmt.Sprintf("ghcr.io/mostlydev/claw-api:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/clawdash:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-wall:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/cllama:%s", DefaultCllamaTag),
		fmt.Sprintf("ghcr.io/mostlydev/hermes-base:%s", DefaultHermesBaseTag),
	}
}
