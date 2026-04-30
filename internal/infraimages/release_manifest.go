package infraimages

import "fmt"

const (
	DefaultClawInfraTag    = "v0.14.1"
	DefaultClawAPITag      = DefaultClawInfraTag
	DefaultClawdashTag     = DefaultClawInfraTag
	DefaultClawWallTag     = DefaultClawInfraTag
	DefaultClawMCPStdioTag = DefaultClawInfraTag
	DefaultCllamaTag       = "v0.6.3"
	DefaultHermesBaseTag   = "v2026.4.23-claw.1"
)

func ReleaseRefs(releaseTag string) []string {
	return []string{
		fmt.Sprintf("ghcr.io/mostlydev/claw-api:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/clawdash:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-wall:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-mcp-stdio:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/cllama:%s", DefaultCllamaTag),
		fmt.Sprintf("ghcr.io/mostlydev/hermes-base:%s", DefaultHermesBaseTag),
	}
}
