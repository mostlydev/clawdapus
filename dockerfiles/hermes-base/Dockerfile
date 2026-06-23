FROM ghcr.io/astral-sh/uv:python3.11-bookworm-slim

ARG GIT_REVISION=unknown
ARG CLAW_SOURCE=local-checkout
ARG CLAW_DIRTY=true
ARG HERMES_UPSTREAM_TAG=v2026.6.19

LABEL claw.component="hermes-base" \
      org.opencontainers.image.revision="${GIT_REVISION}" \
      claw.source="${CLAW_SOURCE}" \
      claw.dirty="${CLAW_DIRTY}"

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    tini \
    procps \
    curl \
    git \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Pin Hermes to the upstream git tag that matches this base image version.
RUN git clone --depth 1 --branch "${HERMES_UPSTREAM_TAG}" https://github.com/NousResearch/hermes-agent.git /opt/hermes-agent \
    && uv pip install --system --no-cache "/opt/hermes-agent[messaging,cron]"

# Apply compatibility patches before the image is sealed
COPY minisweagent_path.py /tmp/minisweagent_path.py
COPY patch-hermes-runtime.py /tmp/patch-hermes-runtime.py
RUN python3 /tmp/patch-hermes-runtime.py

COPY entrypoint.sh /usr/local/bin/hermes-entrypoint
RUN chmod +x /usr/local/bin/hermes-entrypoint

RUN mkdir -p /root/.hermes /workspace /persona

ENV HOME=/root \
    HERMES_HOME=/root/.hermes

WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["hermes-entrypoint"]
