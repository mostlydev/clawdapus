---
layout: home

hero:
  name: Clawdapus
  text: Docker on Rails for Claws
  tagline: Infrastructure-layer governance for AI agent containers. The layer below the framework, where deployment meets governance.
  image:
    src: /clawdapus.png
    alt: Clawdapus
  actions:
    - theme: brand
      text: Get Started
      link: /guide/quickstart
    - theme: alt
      text: View on GitHub
      link: https://github.com/mostlydev/clawdapus

features:
  - icon: "\U0001F419"
    title: Untrusted by Design
    details: Every agent is a container — reproducible, inspectable, diffable, and killable. Purpose is bind-mounted read-only. Survives full container compromise.
  - icon: "\U0001F512"
    title: Credential Starvation
    details: The cllama governance proxy holds the real API keys. Agents get bearer tokens. No credentials means no bypass — every LLM call flows through the proxy.
  - icon: "\U0001F433"
    title: Extends Docker
    details: Clawfile extends Dockerfile. claw-pod.yml extends docker-compose.yml. Eject anytime — you still have working OCI images and compose files.
  - icon: "\U0001F3AD"
    title: 7 Runner Drivers
    details: "OpenClaw, Hermes, NanoClaw, Nanobot, PicoClaw, NullClaw, MicroClaw. Pick your runtime. Same governance layer wraps them all."
  - icon: "\U0001F4E1"
    title: Social Topology
    details: "HANDLE declares platform identity. Every agent's Discord/Telegram/Slack IDs are broadcast pod-wide. Services can @mention bots without hardcoding."
  - icon: "\U0001F9E0"
    title: Master Claw
    details: Delegate fleet oversight to an AI governor. It reads proxy telemetry and autonomously manages budgets, quarantines, and recipe promotions.
---
