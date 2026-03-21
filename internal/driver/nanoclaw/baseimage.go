package nanoclaw

const baseImageTag = "nanoclaw-orchestrator:latest"

const baseImageDockerfile = `FROM node:22-bookworm-slim AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git python3 make g++ \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

RUN git clone --depth 1 https://github.com/qwibitai/nanoclaw.git /src \
    && rm -rf /src/.git

RUN npm ci
RUN npm run build

FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git procps tini \
    && rm -rf /var/lib/apt/lists/*

COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker

RUN npm install -g @anthropic-ai/claude-code

WORKDIR /workspace

COPY --from=builder /src /workspace

RUN mkdir -p /workspace/groups/main /workspace/container/skills

ENTRYPOINT ["/usr/bin/tini", "--", "node", "/workspace/dist/index.js"]
`

func (d *Driver) BaseImage() (string, string) {
	return baseImageTag, baseImageDockerfile
}
