---
name: clawdapus-release
description: >
  Automates cutting a Clawdapus release: runs pre-release checks, coordinates
  with cllama submodule releases if needed, determines the next semver version,
  backfills any missing changelog entries, sweeps docs (CLI reference, README,
  manifesto) for updates tied to the release, writes the new version entry in
  the site changelog, updates the nav dropdown and Latest badge, commits, tags,
  pushes (which triggers goreleaser + site deploy), and rebuilds any affected
  Docker images. Use this skill whenever the user says "release", "cut a
  release", "new version", "update the changelog and tag", "prepare a release",
  or anything about shipping a new version of the claw CLI.
---

# Release

This skill automates the full release pipeline for Clawdapus. The release
workflow is tag-driven: pushing a semver tag on master triggers
`.github/workflows/release.yml` (goreleaser builds binaries and creates the
GitHub release) and `.github/workflows/deploy-site.yml` deploys the updated
site to clawdapus.dev.

`cllama` and the infra Docker images (`cllama`, `claw-wall`, `claw-api`,
`hermes-base`) are on separate release cycles with manual steps — this skill
covers both sides.

Your job is to prepare everything locally, confirm with the user, then push.

## Step 1: Pre-release sanity checks

Before touching anything, verify the working tree and the build:

```bash
# Clean working tree check
git status

# Make sure local tags match GitHub — local tag list can lag badly
git fetch --tags

# Build sanity — catch compile errors before tagging
go build ./...
go vet ./...
```

If the working tree is dirty, decide with the user whether those changes are
part of this release or should be stashed/committed separately. Don't mix
unrelated changes into the release commit.

If `go build` or `go vet` fails, stop and fix it — goreleaser will fail too.

## Step 2: Determine cllama coordination

The `cllama/` submodule has its own tags, GitHub releases, and Docker image
(`ghcr.io/mostlydev/cllama:latest`). If a clawdapus release depends on cllama
changes, release cllama FIRST so the submodule pointer references a tagged,
released commit.

Check whether cllama moved since its last release:

```bash
# Current submodule pointer
git -C cllama rev-parse HEAD

# Latest cllama GitHub release
gh -R mostlydev/cllama release list --limit 3

# Latest cllama tag (local + remote)
git -C cllama tag --sort=-v:refname | head -3

# Commits since last cllama release on the submodule pointer
git -C cllama log <last-cllama-tag>..HEAD --oneline
```

Decision tree:

- **No cllama changes since last release** → skip to Step 3.
- **Submodule moved but all commits are already in a released cllama tag** →
  skip to Step 3.
- **Submodule moved past the latest cllama release** → cut a cllama release
  first (see "Cllama release sub-workflow" below), then continue.
- **A cllama tag exists locally/remotely but has no GitHub release** → backfill
  that release (even if you're also cutting a new one) so the GitHub release
  history matches the tag history.

### Cllama release sub-workflow

1. Pick the next cllama semver (patch / minor) based on the commits.
2. If not already tagged, tag in the submodule:
   ```bash
   git -C cllama tag v0.X.Y
   git -C cllama push origin v0.X.Y
   ```
3. Create the GitHub release with synthesized notes grouped by theme (not raw
   commit list):
   ```bash
   gh -R mostlydev/cllama release create v0.X.Y \
     --title "v0.X.Y — <short headline>" \
     --notes "$(cat <<'EOF'
   ## Highlights

   - **Feature** — user-facing description.

   ## Fixes

   - Fix: brief description.
   EOF
   )"
   ```
4. Build and push the multi-arch Docker image (see Step 9). Do this BEFORE the
   clawdapus release so the clawdapus image fallback (`docker pull
   ghcr.io/mostlydev/cllama:latest`) picks up the new image.
5. Update the submodule pointer in the clawdapus repo if cllama HEAD moved past
   the tag you just released, then `git add cllama && git commit`.

## Step 3: Determine the clawdapus release version

```bash
# Latest tag
git tag --sort=-v:refname | head -1

# Latest GitHub release
gh release list --limit 1

# Commits since last tag
git log <latest-tag>..HEAD --oneline
```

Analyze the commits and propose a version bump:

- **patch** (0.x.Y) — bug fixes, doc updates, dependency bumps, diagnostic
  additions, internal refactors
- **minor** (0.X.0) — new features, new commands, new drivers, capability
  additions, new pod YAML directives, new CLI flags
- **major** (X.0.0) — breaking changes (not yet applicable pre-1.0)

Present the proposed version to the user and wait for confirmation. If the
user already specified a version (e.g. "cut v0.5.0"), use that.

## Step 4: Backfill missing changelog entries

The site changelog (`site/changelog.md`) may be behind GitHub releases. Check:

```bash
# Extract version headers already in the changelog
grep '^## v' site/changelog.md

# Compare against actual GitHub releases
gh release list --limit 20
```

For each release that exists on GitHub but not in the changelog, fetch its
notes and the underlying PRs/issues so you can write user-facing prose:

```bash
gh release view <tag> --json tagName,body,publishedAt
git log <prev-tag>..<tag> --oneline
```

Insert synthesized entries in descending order between "Unreleased" and the
most recent existing entry. Use the same style as existing entries:

```markdown
## v0.X.Y {#v0-X-Y}

*YYYY-MM-DD*

- **Feature name** — concise description of what changed and why it matters.
- Fix: brief description of bug fix.
```

Don't just dump commit hashes — synthesize user-facing prose grouped by theme.
Pull context from referenced PRs/issues (`gh pr view N`, `gh issue view N`)
when the commit message alone doesn't explain the "why".

## Step 5: Write the new version entry

Convert the `## Unreleased` section (if present) into the new version heading.
If there's no Unreleased section, write one from the commits since the last
tag and from the PRs/issues they reference.

The new version gets the `<Badge>` and anchor:

```markdown
## v0.X.Y <Badge type="tip" text="Latest" /> {#v0-X-Y}

*YYYY-MM-DD*

- **Feature** — description.
```

Remove the `<Badge type="tip" text="Latest" />` from whichever older version
currently has it. Only one version should have the Latest badge.

Add a fresh empty `## Unreleased` section at the top (above the new version)
so there's always a place for future notes:

```markdown
## Unreleased

<!-- Nothing yet -->
```

## Step 6: Docs sweep

Release changes often ripple into non-changelog docs. Sweep each of these:

### CLI reference — `site/guide/cli.md`

If the release added, removed, or modified anything observable via the CLI,
update the reference. Check every change category:

- **New subcommands or flags** — add a new section or update the flags table
- **Changed default behavior** — update the "What it does" description
- **New JSON output fields** in `claw audit`, `claw inspect`, `claw ps`,
  `claw health`, `claw doctor` — document them
- **New error conditions or exit codes** — add to the command's description
- **Removed/renamed things** — remove or rename in the docs

Grep for anything that looks stale:

```bash
# Find doc references to CLI flags/subcommands affected by the release
grep -n '<flag-or-command>' site/guide/cli.md
```

### README — `README.md`

Patch releases rarely need README changes. Minor/major releases often do.
Check:

- Quickstart commands still accurate (flags, image names, example paths)
- Feature list / supported drivers list reflects reality
- Version numbers mentioned anywhere (installer, docker image tags)

### Manifesto — `site/manifesto.md` and root `MANIFESTO.md`

Update only when a release changes the project's scope, vision, or core
mechanics (compiled tools, memory plane, fleet governance, etc.). Most
patch releases leave this untouched.

### Guide pages — `site/guide/*.md`

New features often need a new guide page or updates to an existing one:

- New pod YAML directive → update `claw-pod-yml.md` or similar
- New capability (tools, memory, schedule, surfaces) → may need dedicated page
- ADRs in `docs/decisions/` → link from the relevant guide page if the ADR
  changes user-visible behavior

### Other locations to grep

```bash
# Find version references that might need bumping
grep -rn "v0\.X\.Y" site/ README.md examples/ --include='*.md' --include='*.mts'
```

(Package-lock files, go.sum, and generated artifacts are noise — filter them
out.)

## Step 7: Update the nav dropdown version

In `site/.vitepress/config.mts`, find the version string in the nav array:

```ts
{
  text: 'v0.X.Y',  // ← update this
  items: [
```

Replace it with the new version.

## Step 8: Commit, tag, and push

Stage the changed files explicitly — don't use `git add -A`:

```bash
git add site/changelog.md site/.vitepress/config.mts
# Add any docs sweep changes
git add site/guide/cli.md README.md  # only if modified
# Add submodule pointer if cllama moved
git add cllama  # only if modified
```

Commit:

```bash
git commit -m "site: release v0.X.Y

Backfill v0.A.B–v0.C.D changelog entries. Add v0.X.Y with [brief summary].
Update nav dropdown and CLI reference."
```

Tag:

```bash
git tag v0.X.Y
```

Before pushing, confirm with the user: "Ready to push commit + tag. This will
trigger goreleaser (binary release) and site deploy. Go?"

Then push:

```bash
git push origin master --tags
```

If the push is rejected because the remote is ahead, pull with rebase, re-tag
(delete old local tag first), and push again:

```bash
git pull --rebase origin master
git tag -d v0.X.Y
git tag v0.X.Y
git push origin master
git push origin v0.X.Y --force
```

## Step 9: Docker image rebuilds (post-release)

Clawdapus ships several infra images that are NOT auto-built by CI. Rebuild
any that were affected by the release.

Prerequisite: `docker buildx create --name multiarch-builder --use` (one-time
setup). Authenticate to ghcr.io: `gh auth token | docker login ghcr.io -u
<user> --password-stdin`.

### cllama

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/cllama:latest \
  -t ghcr.io/mostlydev/cllama:v0.X.Y \
  --push cllama/
```

### claw-wall

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/claw-wall:latest \
  --push \
  -f dockerfiles/claw-wall/Dockerfile .
```

### claw-api

**Not published to ghcr.io.** Build locally from repo root if users will
consume it:

```bash
docker build -t ghcr.io/mostlydev/claw-api:latest -f dockerfiles/claw-api/Dockerfile .
```

### hermes-base

Tagged per upstream Hermes version, not per clawdapus release:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/mostlydev/hermes-base:v<upstream-tag> \
  --push dockerfiles/hermes-base/
```

New ghcr.io packages default to private — after first push, visit the GitHub
package settings UI and flip them to public.

## Step 10: Verify

After pushing the clawdapus tag, confirm the workflows started:

```bash
gh run list --limit 5
```

Check the GitHub release was created:

```bash
gh release view v0.X.Y
```

Report the status to the user with links:

- Release: `https://github.com/mostlydev/clawdapus/releases/tag/v0.X.Y`
- Site: `https://clawdapus.dev/changelog#v0-X-Y`

## Edge cases

- **No commits since last tag**: Tell the user there's nothing to release.
- **Unreleased section is empty**: Write it from `git log` and PR context.
- **Multiple missing GitHub releases**: Backfill all of them, oldest first.
- **Dirty working tree**: Warn the user before starting. Don't mix unrelated
  changes into the release commit.
- **User wants to skip backfill**: Ask, don't force it.
- **Cllama tag exists but no GitHub release**: Backfill the release — the
  goreleaser pipeline for the clawdapus repo only covers the clawdapus binary,
  not cllama. Cllama releases are manual.
- **Docker buildx builder missing**: `docker buildx create --name
  multiarch-builder --use` before push.
- **ghcr.io package is private**: After first push of a new package, flip it
  to public in the GitHub UI — the Docker image fallback in `claw up` will
  fail otherwise.
- **Rebase moved the tagged commit**: If you had to rebase after tagging, the
  tag points at the old (pre-rebase) commit. Delete the local tag, re-create
  it, and force-push just the tag. If goreleaser already created a partial
  release from the stale tag, delete it, then delete and re-push the remote
  tag to retrigger:
  ```bash
  gh release delete v0.X.Y --yes
  git push origin :refs/tags/v0.X.Y
  git push origin v0.X.Y
  ```
- **Submodule pointer lags the cllama tag**: If the clawdapus submodule points
  at an untagged cllama commit, bump it to the release tag before cutting the
  clawdapus release so users running `claw up` against the published
  clawdapus binary consume a released cllama image.
