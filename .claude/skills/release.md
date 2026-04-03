---
name: release
description: >
  Automates cutting a Clawdapus release: determines the next semver version,
  backfills any missing changelog entries from GitHub releases, writes the new
  version entry in the site changelog, updates the nav dropdown and Latest badge,
  commits, tags, and pushes (which triggers goreleaser + site deploy).
  Use this skill whenever the user says "release", "cut a release", "new version",
  "update the changelog and tag", "prepare a release", or anything about shipping
  a new version of the claw CLI.
---

# Release

This skill automates the full release pipeline for Clawdapus. The release
workflow is tag-driven: pushing a semver tag triggers `.github/workflows/release.yml`
(goreleaser builds binaries and creates the GitHub release) and
`.github/workflows/deploy-site.yml` deploys the updated site to clawdapus.dev.

Your job is to prepare everything locally, confirm with the user, then push.

## Step 1: Determine the release version

Run these commands to understand the current state:

```bash
# Latest tag
git tag --sort=-v:refname | head -1

# Latest GitHub release
gh release list --limit 1

# Commits since last tag
git log <latest-tag>..HEAD --oneline
```

Analyze the commits and propose a version bump:
- **patch** (0.x.Y) — bug fixes, doc updates, dependency bumps
- **minor** (0.X.0) — new features, new commands, new drivers, capability additions
- **major** (X.0.0) — breaking changes (not yet applicable pre-1.0)

Present the proposed version to the user and wait for confirmation. If the user
already specified a version (e.g. "cut v0.5.0"), use that.

## Step 2: Backfill missing changelog entries

The site changelog (`site/changelog.md`) may be behind GitHub releases. Check:

```bash
# Extract version headers already in the changelog
grep '^## v' site/changelog.md
```

Compare against actual GitHub releases:

```bash
gh release list --limit 20
```

For each release that exists on GitHub but not in the changelog, fetch its notes:

```bash
gh release view <tag> --json tagName,body,publishedAt
```

Write human-readable changelog entries for each missing version. Insert them in
descending order between the "Unreleased" section (or the new version heading)
and the most recent existing entry. Use the same style as existing entries:

```markdown
## v0.X.Y {#v0-X-Y}

*YYYY-MM-DD*

- **Feature name** — concise description of what changed and why it matters.
- Fix: brief description of bug fix.
```

Don't just dump commit hashes — synthesize the release notes into user-facing
changelog prose grouped by theme.

## Step 3: Write the new version entry

Convert the `## Unreleased` section (if present) into the new version heading.
If there's no Unreleased section, write one from the commits since the last tag.

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

## Step 4: Update the nav dropdown version

In `site/.vitepress/config.mts`, find the version string in the nav array:

```ts
{
  text: 'v0.X.Y',  // ← update this
  items: [
```

Replace it with the new version.

## Step 5: Commit, tag, and push

Stage only the changed files:

```bash
git add site/changelog.md site/.vitepress/config.mts
```

Commit with a message like:

```
site: release v0.X.Y

Backfill v0.A.B–v0.C.D changelog entries. Add v0.X.Y with [brief summary].
Update nav dropdown to v0.X.Y.
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

## Step 6: Verify

After pushing, confirm the workflows started:

```bash
gh run list --limit 5
```

Report the status to the user and link to the release page:
`https://github.com/mostlydev/clawdapus/releases/tag/v0.X.Y`

## Edge cases

- **No commits since last tag**: Tell the user there's nothing to release.
- **Unreleased section is empty**: Write it from `git log` commits.
- **Multiple missing GitHub releases**: Backfill all of them, oldest first.
- **Dirty working tree**: Warn the user about uncommitted changes before starting. Don't mix unrelated changes into the release commit.
- **User wants to skip backfill**: That's fine — ask, don't force it.
- **Rebase moved the tagged commit**: If you had to rebase after tagging, the tag
  points at the old (pre-rebase) commit. Delete the local tag, re-create it, and
  force-push just the tag. If goreleaser already created a partial release from
  the stale tag, delete it with `gh release delete <tag> --yes`, then delete and
  re-push the remote tag to retrigger:
  ```bash
  gh release delete v0.X.Y --yes
  git push origin :refs/tags/v0.X.Y
  git push origin v0.X.Y
  ```
