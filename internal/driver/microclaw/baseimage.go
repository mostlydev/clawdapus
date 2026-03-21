package microclaw

const baseImageTag = "microclaw:latest"

const baseImageDockerfile = `FROM ghcr.io/microclaw/microclaw:latest

USER root

RUN apt-get update && apt-get install -y --no-install-recommends procps \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /app/config /claw-data

USER microclaw
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}
