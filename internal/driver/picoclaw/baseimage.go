package picoclaw

const baseImageTag = "picoclaw:latest"

const baseImageDockerfile = `FROM docker.io/sipeed/picoclaw:latest

LABEL org.opencontainers.image.source="https://github.com/sipeed/picoclaw"
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}

func (d *Driver) RunnerAlias() string {
	return "picoclaw"
}
