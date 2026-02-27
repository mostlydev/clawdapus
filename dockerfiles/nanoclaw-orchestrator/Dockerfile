# NanoClaw Orchestrator — built from stock nanoclaw + Clawdapus governance patch
#
# Clones upstream nanoclaw, applies a small patch to forward ANTHROPIC_BASE_URL
# and CLAW_NETWORK to ephemeral agent-runner containers, then builds.
# No fork required.
ARG NANOCLAW_REF=main
ARG NANOCLAW_REPO=https://github.com/qwibitai/nanoclaw.git

FROM node:22-slim AS builder
ARG NANOCLAW_REF
ARG NANOCLAW_REPO
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone --depth 1 --branch ${NANOCLAW_REF} ${NANOCLAW_REPO} .
COPY governance.patch /tmp/governance.patch
RUN git apply /tmp/governance.patch
RUN npm ci
RUN npx tsc

FROM node:22-slim
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
RUN apt-get update && apt-get install -y --no-install-recommends python3 make g++ && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /src/dist/ ./dist/
COPY --from=builder /src/node_modules/ ./node_modules/
COPY --from=builder /src/package.json ./
RUN npm rebuild better-sqlite3
COPY --from=builder /src/container/ ./container/
RUN mkdir -p /workspace/groups/main /workspace/data /workspace/store
RUN rm -rf /var/lib/apt/lists/* && apt-get purge -y python3 make g++ && apt-get autoremove -y || true
WORKDIR /workspace
ENTRYPOINT ["node", "/app/dist/index.js"]
