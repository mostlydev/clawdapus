# Python 3.14 is required: hermes-agent v0.22.x gates its core dependencies on
# python_version >= '3.14'. Trixie ships a SQLite without the WAL-reset bug that
# forces Hermes into journal_mode=DELETE on bookworm.
FROM ghcr.io/astral-sh/uv:python3.14-trixie-slim@sha256:db955a4c5fd85c9e08ae2c02f3fcedf76765aebc7c9bc10c55a6bbcc1b0510be

ARG GIT_REVISION=unknown
ARG CLAW_SOURCE=local-checkout
ARG CLAW_DIRTY=true
ARG HERMES_UPSTREAM_TAG=v0.22.2
ARG HERMES_UPSTREAM_COMMIT=d923a5eb487a17c0a68801bba9ef0f56c81040a8

LABEL claw.component="hermes-base" \
      org.opencontainers.image.revision="${GIT_REVISION}" \
      claw.source="${CLAW_SOURCE}" \
      claw.dirty="${CLAW_DIRTY}" \
      claw.hermes.upstream-tag="${HERMES_UPSTREAM_TAG}" \
      claw.hermes.upstream-commit="${HERMES_UPSTREAM_COMMIT}"

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

# Pin Hermes to the upstream tag AND commit; a moved tag fails the build.
# Upstream setup.py refuses wheel/sdist builds outside its Nix derivation
# (HERMES_NIX_BUILD), so Hermes is installed editable from the checkout. The
# .git directory is kept so `hermes --version` resolves the release tag.
RUN git clone --depth 1 --branch "${HERMES_UPSTREAM_TAG}" https://github.com/NousResearch/hermes-agent.git /opt/hermes-agent \
    && actual="$(git -C /opt/hermes-agent rev-parse HEAD)" \
    && if [ "${actual}" != "${HERMES_UPSTREAM_COMMIT}" ]; then \
         echo "hermes-agent ${HERMES_UPSTREAM_TAG} resolved to ${actual}, expected ${HERMES_UPSTREAM_COMMIT}" >&2; \
         exit 1; \
       fi \
    && uv pip install --system --no-cache -e "/opt/hermes-agent[messaging,cron]"

# Apply compatibility patches before the image is sealed
COPY patch-hermes-runtime.py /tmp/patch-hermes-runtime.py
RUN python3 /tmp/patch-hermes-runtime.py /opt/hermes-agent \
    && rm -f /tmp/patch-hermes-runtime.py

COPY entrypoint.sh /usr/local/bin/hermes-entrypoint
COPY claw-hermes-wake.sh /usr/local/bin/claw-hermes-wake
RUN chmod 0755 /usr/local/bin/hermes-entrypoint /usr/local/bin/claw-hermes-wake

RUN mkdir -p /root/.hermes /workspace /persona

ENV HOME=/root \
    HERMES_HOME=/root/.hermes

WORKDIR /workspace
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["hermes-entrypoint"]
