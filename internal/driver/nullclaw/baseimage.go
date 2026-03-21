package nullclaw

const baseImageTag = "nullclaw:latest"

const baseImageDockerfile = `FROM ghcr.io/nullclaw/nullclaw:latest

LABEL org.opencontainers.image.source="https://github.com/nullclaw/nullclaw"
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}
