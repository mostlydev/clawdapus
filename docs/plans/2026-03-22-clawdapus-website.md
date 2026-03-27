# Clawdapus Website Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Ship a VitePress-powered marketing + docs site for Clawdapus at `site/`, deployable via GitHub Pages.

**Architecture:** Static site using VitePress with the default theme. Landing page with hero, feature grid, and code examples. Docs section covering quickstart, architecture, CLI reference. Deep ocean color theme (teal/purple). GitHub Actions deploys to `gh-pages` branch.

**Tech Stack:** VitePress, Node.js, GitHub Actions

---

### Task 1: Scaffold VitePress project

**Files:**
- Create: `site/package.json`
- Create: `site/.vitepress/config.mts`
- Create: `site/index.md`

**Step 1: Create package.json**

```json
{
  "name": "clawdapus-site",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vitepress dev",
    "build": "vitepress build",
    "preview": "vitepress preview"
  },
  "devDependencies": {
    "vitepress": "^1.6.3"
  }
}
```

**Step 2: Create VitePress config**

Create `site/.vitepress/config.mts`:

```typescript
import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'Clawdapus',
  description: 'Infrastructure-layer governance for AI agent containers',
  head: [
    ['link', { rel: 'icon', href: '/clawdapus.png' }],
  ],

  themeConfig: {
    logo: '/clawdapus.png',

    nav: [
      { text: 'Guide', link: '/guide/what-is-clawdapus' },
      { text: 'Reference', link: '/reference/cli' },
      {
        text: 'v0.3.2',
        items: [
          { text: 'Changelog', link: '/changelog' },
          { text: 'Manifesto', link: '/manifesto' },
        ]
      }
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Introduction',
          items: [
            { text: 'What is Clawdapus?', link: '/guide/what-is-clawdapus' },
            { text: 'Quickstart', link: '/guide/quickstart' },
          ]
        },
        {
          text: 'Core Concepts',
          items: [
            { text: 'The Anatomy of a Claw', link: '/guide/anatomy' },
            { text: 'Clawfile', link: '/guide/clawfile' },
            { text: 'Pod YAML', link: '/guide/pod-yaml' },
            { text: 'cllama Governance Proxy', link: '/guide/cllama' },
            { text: 'Social Topology', link: '/guide/social-topology' },
          ]
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'CLI Commands', link: '/reference/cli' },
            { text: 'Clawfile Directives', link: '/reference/clawfile-directives' },
            { text: 'Driver Support Matrix', link: '/reference/drivers' },
            { text: 'cllama Spec', link: '/reference/cllama-spec' },
          ]
        }
      ]
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/mostlydev/clawdapus' },
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2025-present Mostly Dev'
    },

    search: {
      provider: 'local'
    }
  }
})
```

**Step 3: Create placeholder landing page**

Create `site/index.md`:

```markdown
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
  - icon: 🐙
    title: Untrusted by Design
    details: Every agent is a container — reproducible, inspectable, diffable, and killable. Purpose is bind-mounted read-only. Survives full container compromise.
  - icon: 🔒
    title: Credential Starvation
    details: The cllama governance proxy holds the real API keys. Agents get bearer tokens. No credentials means no bypass — every LLM call flows through the proxy.
  - icon: 🐳
    title: Extends Docker
    details: Clawfile extends Dockerfile. claw-pod.yml extends docker-compose.yml. Eject anytime — you still have working OCI images and compose files.
  - icon: 🎭
    title: 7 Runner Drivers
    details: OpenClaw, Hermes, NanoClaw, Nanobot, PicoClaw, NullClaw, MicroClaw. Pick your runtime. Same governance layer wraps them all.
  - icon: 📡
    title: Social Topology
    details: HANDLE declares platform identity. Every agent's Discord/Telegram/Slack IDs are broadcast pod-wide. Services can @mention bots without hardcoding.
  - icon: 🧠
    title: Master Claw
    details: Delegate fleet oversight to an AI governor. It reads proxy telemetry and autonomously manages budgets, quarantines, and recipe promotions.
---
```

**Step 4: Copy logo to public directory**

```bash
mkdir -p site/public
cp docs/art/clawdapus.png site/public/clawdapus.png
```

**Step 5: Install dependencies and verify dev server starts**

```bash
cd site && npm install && npx vitepress dev --port 5173 &
# Visit http://localhost:5173, verify hero page renders
# Kill the dev server
```

**Step 6: Commit**

```bash
git add site/
git commit -m "feat(site): scaffold VitePress project with landing page"
```

---

### Task 2: Custom theme colors

**Files:**
- Create: `site/.vitepress/theme/index.ts`
- Create: `site/.vitepress/theme/custom.css`

**Step 1: Create custom CSS with ocean theme**

Create `site/.vitepress/theme/custom.css`:

```css
:root {
  /* Deep ocean teal */
  --vp-c-brand-1: #0d9488;
  --vp-c-brand-2: #14b8a6;
  --vp-c-brand-3: #2dd4bf;
  --vp-c-brand-soft: rgba(20, 184, 166, 0.14);

  /* Hero name gradient */
  --vp-home-hero-name-color: transparent;
  --vp-home-hero-name-background: -webkit-linear-gradient(120deg, #0d9488 30%, #a78bfa);

  /* Hero image glow */
  --vp-home-hero-image-background-image: linear-gradient(-45deg, #0d948880 50%, #a78bfa80 50%);
  --vp-home-hero-image-filter: blur(44px);
}

.dark {
  --vp-c-brand-1: #14b8a6;
  --vp-c-brand-2: #2dd4bf;
  --vp-c-brand-3: #5eead4;
  --vp-c-brand-soft: rgba(20, 184, 166, 0.16);
}

/* Slightly larger hero image */
:root {
  --vp-home-hero-image-background-image: linear-gradient(-45deg, #0d948880 50%, #a78bfa80 50%);
  --vp-home-hero-image-filter: blur(56px);
}
```

**Step 2: Create theme index that extends default**

Create `site/.vitepress/theme/index.ts`:

```typescript
import DefaultTheme from 'vitepress/theme'
import './custom.css'

export default DefaultTheme
```

**Step 3: Verify colors render in dev server**

```bash
cd site && npx vitepress dev --port 5173 &
# Check hero gradient, button colors, dark mode toggle
```

**Step 4: Commit**

```bash
git add site/.vitepress/theme/
git commit -m "feat(site): add ocean teal/purple theme"
```

---

### Task 3: Landing page code examples section

**Files:**
- Modify: `site/index.md`

**Step 1: Add code examples below the features frontmatter**

Append after the `---` closing the frontmatter in `site/index.md`:

````markdown

## What It Looks Like

<div class="code-examples">

### The Image — `Clawfile`

```dockerfile
FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4
CLLAMA passthrough

HANDLE discord
INVOKE 15 8 * * 1-5  pre-market

SURFACE service://trading-api
SURFACE volume://shared-research read-write
```

### The Deployment — `claw-pod.yml`

```yaml
x-claw:
  pod: trading-desk
  master: octopus
  cllama-defaults:
    proxy: [passthrough]
    env:
      OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
  surfaces-defaults:
    - "service://trading-api"
    - "volume://shared-research read-write"

services:
  tiverton:
    image: trading-desk-tiverton:latest
    build:
      context: ./agents/tiverton
    x-claw:
      agent: ./agents/tiverton/AGENTS.md
      handles:
        discord:
          id: "${TIVERTON_DISCORD_ID}"
          username: "tiverton"
```

### Five Minutes to Running

```bash
curl -sSL https://raw.githubusercontent.com/mostlydev/clawdapus/master/install.sh | sh
git clone https://github.com/mostlydev/clawdapus.git
cd clawdapus/examples/quickstart
cp .env.example .env   # add your keys
claw build -t quickstart-assistant:latest ./agents/assistant
claw up -f claw-pod.yml -d
claw health -f claw-pod.yml  # ✓ all healthy
```

</div>
````

**Step 2: Verify code blocks render with syntax highlighting**

**Step 3: Commit**

```bash
git add site/index.md
git commit -m "feat(site): add code examples to landing page"
```

---

### Task 4: Guide pages — What is Clawdapus + Quickstart

**Files:**
- Create: `site/guide/what-is-clawdapus.md`
- Create: `site/guide/quickstart.md`

**Step 1: Create "What is Clawdapus?" page**

Create `site/guide/what-is-clawdapus.md` — adapted from MANIFESTO.md Part 1 and README intro. Cover:

- The thesis: agent as untrusted workload
- What it is not (not a framework, not a bot builder)
- The Docker analogy (Clawfile ↔ Dockerfile, claw-pod.yml ↔ docker-compose.yml)
- Core principles (the 8 principles from README)
- Current status table

**Step 2: Create Quickstart page**

Create `site/guide/quickstart.md` — adapted from README quickstart section. Cover:

- Prerequisites (Docker Desktop, OpenRouter key, Discord bot token)
- Install
- Clone + configure
- Build + launch
- Verify
- Dashboard ports (8181 cllama, 8082 clawdash)
- `claw init` alternative scaffold path

**Step 3: Verify both pages render and sidebar nav works**

**Step 4: Commit**

```bash
git add site/guide/
git commit -m "feat(site): add intro and quickstart guide pages"
```

---

### Task 5: Guide pages — Anatomy, Clawfile, cllama

**Files:**
- Create: `site/guide/anatomy.md`
- Create: `site/guide/clawfile.md`
- Create: `site/guide/pod-yaml.md`
- Create: `site/guide/cllama.md`
- Create: `site/guide/social-topology.md`

**Step 1: Create anatomy page**

Adapted from MANIFESTO.md Part 2 — the four components (runner, contract, persona, cllama). Include the mermaid diagram from README.

**Step 2: Create Clawfile page**

Adapted from README "Clawfile Directives" — the directive table, a full example, explanation of transpilation to Dockerfile.

**Step 3: Create pod-yaml page**

Adapted from README "claw-pod.yml" section — pod-level x-claw, services, defaults+overrides, spread syntax.

**Step 4: Create cllama page**

Adapted from README "cllama: The Governance Proxy" — credential starvation, identity resolution, cost accounting, audit logging, dashboard.

**Step 5: Create social topology page**

Adapted from README "Social Topology" — HANDLE, env var broadcasting, mentionPatterns, handles-defaults.

**Step 6: Verify all pages render with sidebar navigation**

**Step 7: Commit**

```bash
git add site/guide/
git commit -m "feat(site): add core concept guide pages"
```

---

### Task 6: Reference pages

**Files:**
- Create: `site/reference/cli.md`
- Create: `site/reference/clawfile-directives.md`
- Create: `site/reference/drivers.md`
- Create: `site/reference/cllama-spec.md`

**Step 1: Create CLI reference**

Document each command from CLAUDE.md: `claw build`, `claw up`, `claw down`, `claw ps`, `claw logs`, `claw health`, `claw inspect`, `claw doctor`, `claw init`, `claw agent add`, `claw compose`. Include flags where known from code.

**Step 2: Create Clawfile directives reference**

The full directive table from README plus detailed per-directive docs.

**Step 3: Create driver support matrix**

The driver comparison table from README.

**Step 4: Create cllama spec page**

Summary with link to full spec. Key sections: transport, identity resolution, telemetry format.

**Step 5: Verify all reference pages render**

**Step 6: Commit**

```bash
git add site/reference/
git commit -m "feat(site): add reference pages"
```

---

### Task 7: Manifesto + Changelog pages

**Files:**
- Create: `site/manifesto.md`
- Create: `site/changelog.md`

**Step 1: Create manifesto page**

Adapted from MANIFESTO.md — the full vision document, formatted for the site.

**Step 2: Create changelog page**

Placeholder with current status table from README and link to GitHub releases.

**Step 3: Commit**

```bash
git add site/manifesto.md site/changelog.md
git commit -m "feat(site): add manifesto and changelog pages"
```

---

### Task 8: GitHub Actions deployment

**Files:**
- Create: `.github/workflows/deploy-site.yml`

**Step 1: Create the workflow**

```yaml
name: Deploy Site

on:
  push:
    branches: [master]
    paths:
      - 'site/**'
      - '.github/workflows/deploy-site.yml'
  workflow_dispatch:

permissions:
  contents: read
  pages: write
  id-token: write

concurrency:
  group: pages
  cancel-in-progress: false

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
          cache-dependency-path: site/package-lock.json
      - name: Install dependencies
        run: npm ci
        working-directory: site
      - name: Build
        run: npm run build
        working-directory: site
      - uses: actions/upload-pages-artifact@v3
        with:
          path: site/.vitepress/dist

  deploy:
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    needs: build
    runs-on: ubuntu-latest
    steps:
      - name: Deploy to GitHub Pages
        id: deployment
        uses: actions/deploy-pages@v4
```

**Step 2: Add site/.gitignore**

Create `site/.gitignore`:

```
node_modules
.vitepress/cache
.vitepress/dist
```

**Step 3: Commit**

```bash
git add .github/workflows/deploy-site.yml site/.gitignore
git commit -m "ci: add GitHub Pages deployment workflow for site"
```

---

### Task 9: Final polish and verify build

**Step 1: Run production build locally**

```bash
cd site && npm run build && npm run preview
# Verify all pages, navigation, search, dark mode
```

**Step 2: Fix any build warnings or broken links**

**Step 3: Final commit if needed**

**Step 4: Add `site/` entry to root .gitignore if node_modules leak**

---

### Task 10: Update root README with site link

**Files:**
- Modify: `README.md`

**Step 1: Add docs site link to README**

Add near the top, after the logo/tagline:

```markdown
📖 **[Documentation](https://mostlydev.github.io/clawdapus/)** | 🚀 **[Quickstart](https://mostlydev.github.io/clawdapus/guide/quickstart)**
```

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add website link to README"
```
