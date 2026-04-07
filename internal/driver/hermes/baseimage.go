package hermes

const UpstreamTag = "v2026.3.17"

var BaseImageTag = "ghcr.io/mostlydev/hermes-base:" + UpstreamTag

const baseImageDockerfile = `FROM ghcr.io/astral-sh/uv:python3.11-bookworm-slim

ARG HERMES_UPSTREAM_TAG=` + UpstreamTag + `

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq procps tini \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch "${HERMES_UPSTREAM_TAG}" https://github.com/NousResearch/hermes-agent.git /opt/hermes-agent \
    && uv pip install --system --no-cache "/opt/hermes-agent[messaging,cron]"

RUN mkdir -p /root/.hermes /workspace /persona

ENV HOME=/root \
    HERMES_HOME=/root/.hermes

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "hermes"]
CMD ["gateway", "start"]
`

func (d *Driver) BaseImage() (string, string) {
	return BaseImageTag, baseImageDockerfile
}
