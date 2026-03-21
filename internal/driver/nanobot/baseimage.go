package nanobot

const baseImageTag = "nanobot:latest"

const baseImageDockerfile = `FROM ghcr.io/astral-sh/uv:python3.12-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git jq procps tini \
    && rm -rf /var/lib/apt/lists/*

RUN uv pip install --system --no-cache nanobot-ai

RUN mkdir -p /root/.nanobot/workspace/skills

ENV HOME=/root

WORKDIR /root/.nanobot/workspace

ENTRYPOINT ["/usr/bin/tini", "--", "nanobot"]
CMD ["gateway"]
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}
