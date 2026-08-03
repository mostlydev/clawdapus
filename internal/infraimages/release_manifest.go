package infraimages

import "fmt"

const (
	DefaultClawInfraTag         = "v0.28.0"
	DefaultClawAPITag           = DefaultClawInfraTag
	DefaultClawdashTag          = DefaultClawInfraTag
	DefaultClawWallTag          = DefaultClawInfraTag
	DefaultClawChannelMemoryTag = DefaultClawInfraTag
	DefaultClawMCPStdioTag      = DefaultClawInfraTag
	DefaultCllamaTag            = "v0.10.0"
	DefaultHermesBaseTag        = "v2026.6.19-claw.3"
)

func ReleaseRefs(releaseTag string) []string {
	return []string{
		fmt.Sprintf("ghcr.io/mostlydev/claw-api:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/clawdash:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-wall:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-channel-memory:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/claw-mcp-stdio:%s", releaseTag),
		fmt.Sprintf("ghcr.io/mostlydev/cllama:%s", DefaultCllamaTag),
		fmt.Sprintf("ghcr.io/mostlydev/hermes-base:%s", DefaultHermesBaseTag),
	}
}
