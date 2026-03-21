package hermes

const baseImageTag = "hermes:latest"

const baseImageDockerfile = `FROM ghcr.io/astral-sh/uv:python3.11-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash ca-certificates curl git jq procps tini \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 https://github.com/NousResearch/hermes-agent.git /opt/hermes-agent \
    && uv pip install --system --no-cache "/opt/hermes-agent[messaging,cron]"

RUN mkdir -p /root/.hermes /workspace /persona

ENV HOME=/root \
    HERMES_HOME=/root/.hermes

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "hermes"]
CMD ["gateway", "start"]
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}
