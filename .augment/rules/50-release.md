---
description: Release workflow, version tagging, Homebrew tap updates, and CI/CD for releases
globs:
  - ".github/workflows/release.yml"
  - ".github/workflows/homebrew.yml"
  - "cliff.toml"
keywords:
  - release
  - tag
  - version
  - homebrew
  - brew
  - changelog
---

# Release Workflow

## Creating a New Release

### Step 1: Determine Version

1. Get the latest tag:
   ```bash
   git describe --tags --abbrev=0
   ```

2. Analyze commits since the last tag using conventional commit prefixes:
   - `feat:` commits → MINOR version bump (0.X.0)
   - `fix:` commits → PATCH version bump (0.0.X)
   - Breaking changes (`!` or `BREAKING CHANGE:`) → MAJOR version bump (X.0.0)

3. List commits since last tag:
   ```bash
   git log $(git describe --tags --abbrev=0)..HEAD --oneline --pretty=format:"%s"
   ```

### Step 2: Create & Push Tag

```bash
git tag -a v0.X.Y -m "Release v0.X.Y"
git push origin v0.X.Y
```

### Step 3: Monitor Release CI

```bash
# Watch the release workflow
gh run list --limit 5
gh run watch <run-id> --exit-status
```

### Step 4: Trigger Homebrew Update

**Important**: The Homebrew workflow does NOT auto-trigger from releases created by GitHub Actions (due to `GITHUB_TOKEN` security restrictions). You must manually trigger it:

```bash
gh workflow run homebrew.yml --field version=v0.X.Y
```

Then verify the update:
```bash
gh run list --workflow=homebrew.yml --limit 3
gh run watch <run-id> --exit-status
```

### Step 5: Verify Homebrew Tap

Check that the tap was updated:
- https://github.com/inercia/homebrew-mitto/blob/main/Formula/mitto.rb
- https://github.com/inercia/homebrew-mitto/blob/main/Casks/mitto.rb

## Fixing Failed Releases

If the release workflow fails:

```bash
# Delete the tag locally and remotely
git tag -d v0.X.Y
git push origin :refs/tags/v0.X.Y

# Fix the issue, commit, push
git add .
git commit -m "fix: description of fix"
git push origin main

# Recreate and push the tag
git tag -a v0.X.Y -m "Release v0.X.Y"
git push origin v0.X.Y
```

## Build Artifacts

The release workflow produces:

| Artifact | Description |
|----------|-------------|
| `mitto-darwin-amd64.tar.gz` | macOS CLI (Intel) |
| `mitto-darwin-arm64.tar.gz` | macOS CLI (Apple Silicon) |
| `mitto-linux-amd64.tar.gz` | Linux CLI (x86_64) |
| `mitto-linux-arm64.tar.gz` | Linux CLI (ARM64) |
| `Mitto-darwin-amd64.zip` | macOS App (Intel) |
| `Mitto-darwin-arm64.zip` | macOS App (Apple Silicon) |

## Workflow Configuration

### Go Version

The release workflow uses `go-version-file: 'go.mod'` to read the Go version from `go.mod`. This ensures consistency with the `tests.yml` workflow.

### golangci-lint

Use `version: latest` for golangci-lint to ensure compatibility with the Go version in `go.mod`.

### Known Issues

1. **git-cliff GitHub Integration**: Cannot access GitHub API for private repos with `GITHUB_TOKEN`. The workflow disables GitHub integration to avoid 403 errors.

2. **Homebrew Auto-Update**: Workflows triggered by `GITHUB_TOKEN` don't trigger other workflows (GitHub security feature). Must manually trigger `homebrew.yml`.

## Homebrew Installation

After release, users can install with:

```bash
# Add tap (first time only)
brew tap inercia/mitto

# CLI only
brew install inercia/mitto/mitto

# macOS app + CLI
brew install --cask inercia/mitto/mitto

# Upgrade existing installation
brew upgrade mitto
```

