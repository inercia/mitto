# Homebrew Distribution for Mitto

This directory contains the configuration and tooling for distributing Mitto via [Homebrew](https://brew.sh).

## Quick Start

### Testing Locally (Recommended First Step)

Before setting up the tap, verify everything works:

```bash
# Run the full test suite (generates, style-checks, installs, and cleans up)
make homebrew-test

# Or test a specific version
make homebrew-test HOMEBREW_VERSION=v0.0.14
```

This will:
1. Generate the formula and cask from the latest release
2. Run Homebrew style checks
3. Create a temporary local tap
4. Install the cask (which includes the CLI)
5. Verify both CLI and macOS app work
6. Clean up everything

---

## What You Need To Do (Manual Steps)

### One-Time Setup: Create the Tap Repository

1. **Create a new GitHub repository** named `homebrew-mitto`:
   - Go to: https://github.com/new
   - Repository name: `homebrew-mitto`
   - Make it **public**
   - Initialize with a README

2. **Clone and set up the structure**:
   ```bash
   git clone https://github.com/inercia/homebrew-mitto
   cd homebrew-mitto
   mkdir -p Formula Casks
   git add .
   git commit -m "Initialize tap structure"
   git push
   ```

### One-Time Setup: Enable Automatic Tap Updates (Recommended)

To have GitHub Actions automatically update the tap on each release:

1. **Create a Personal Access Token (PAT)**:
   - Go to: https://github.com/settings/tokens?type=beta (Fine-grained tokens)
   - Click "Generate new token"
   - Name: `homebrew-tap-update`
   - Expiration: Choose an appropriate duration
   - Repository access: Select "Only select repositories" → `homebrew-mitto`
   - Permissions:
     - Contents: Read and write
   - Click "Generate token" and copy it

2. **Add the token as a repository secret**:
   - Go to: https://github.com/inercia/mitto/settings/secrets/actions
   - Click "New repository secret"
   - Name: `HOMEBREW_TAP_TOKEN`
   - Value: Paste the token from step 1
   - Click "Add secret"

Once configured, the tap will be updated automatically on each release!

### For Each Release (Automatic)

If `HOMEBREW_TAP_TOKEN` is configured:

1. **Tag and push a release** - that's it!
2. GitHub Actions will automatically:
   - Generate the formula and cask
   - Push them to the `homebrew-mitto` repository
   - Users can immediately `brew upgrade mitto`

### For Each Release (Manual Fallback)

If `HOMEBREW_TAP_TOKEN` is not configured, manually update the tap:

1. **Wait for GitHub Actions** to complete
2. **Download the generated files** from GitHub Actions:
   ```
   Actions → "Update Homebrew Formula" → (latest run) → Artifacts
   ```
3. **Update your tap repository**:
   ```bash
   cd homebrew-mitto
   cp ~/Downloads/mitto.rb Formula/mitto.rb
   cp ~/Downloads/mitto.cask.rb Casks/mitto.rb
   git add -A && git commit -m "Update mitto to v1.0.0" && git push
   ```

---

## What's Automated (GitHub Actions)

The workflow at `.github/workflows/homebrew.yml` automatically:

| Step | Description |
|------|-------------|
| 1. Trigger | Runs when a new release is published |
| 2. Download | Fetches all release artifacts (CLI + macOS app) |
| 3. Checksum | Calculates SHA256 for each platform |
| 4. Generate | Creates both formula and cask files |
| 5. Upload | Saves as workflow artifacts (backup) |
| 6. Push | Updates the `homebrew-mitto` tap repository (if token configured) |

### Manual Trigger

You can also trigger the workflow manually:
```bash
gh workflow run homebrew.yml -f version=v1.2.3
```

### Required Secret

| Secret | Description |
|--------|-------------|
| `HOMEBREW_TAP_TOKEN` | Personal Access Token with write access to `homebrew-mitto` repo |

Without this secret, the workflow will still generate the files as artifacts, but you'll need to manually copy them to the tap.

---

## Testing

### Using Makefile (Recommended)

| Command | Description |
|---------|-------------|
| `make homebrew-test` | Full test: generate, style-check, install, verify, cleanup |
| `make homebrew-test-style` | Only run `brew style` checks |
| `make homebrew-test-install` | Test CLI formula installation only |
| `make homebrew-test-cask` | Test cask installation (includes CLI) |
| `make homebrew-generate` | Generate files to `build/homebrew/` |
| `make homebrew-clean` | Clean up test artifacts |

#### Examples

```bash
# Test with latest release
make homebrew-test

# Test with specific version
make homebrew-test HOMEBREW_VERSION=v0.0.14

# Just generate files (no installation)
make homebrew-generate HOMEBREW_VERSION=v0.0.14

# Just check style
make homebrew-test-style
```

### Manual Testing

If you prefer to test manually:

```bash
# 1. Generate formula and cask
./platform/homebrew/generate-formula.sh --all v0.0.14

# 2. Check style
brew style mitto.rb
brew style mitto.cask.rb

# 3. Create local tap
brew tap-new --no-git local/mitto-test
mkdir -p /opt/homebrew/Library/Taps/local/homebrew-mitto-test/Casks
cp mitto.rb /opt/homebrew/Library/Taps/local/homebrew-mitto-test/Formula/
cp mitto.cask.rb /opt/homebrew/Library/Taps/local/homebrew-mitto-test/Casks/mitto.rb

# 4. Install and verify
brew install --cask local/mitto-test/mitto
which mitto
ls /Applications/Mitto.app

# 5. Clean up
brew uninstall --cask mitto
brew uninstall mitto
brew untap local/mitto-test
rm mitto.rb mitto.cask.rb
```

---

## User Installation

Once the tap is set up, users can install with:

```bash
# Add the tap (one-time)
brew tap inercia/mitto

# Install CLI only (works on macOS and Linux)
brew install mitto

# Install macOS app + CLI (macOS only)
brew install --cask mitto
```

Or in one command:
```bash
brew install inercia/mitto/mitto              # CLI only
brew install --cask inercia/mitto/mitto       # macOS app + CLI
```

---

## Files in This Directory

| File | Purpose |
|------|---------|
| `mitto.rb.template` | Template for generating CLI formula |
| `mitto.rb.cask.template` | Template for generating macOS app cask |
| `generate-formula.sh` | Script to generate formula and/or cask |
| `README.md` | This documentation |

### generate-formula.sh Usage

```bash
# Generate CLI formula only (to stdout)
./generate-formula.sh v1.0.0 > mitto.rb

# Generate macOS app cask only (to stdout)
./generate-formula.sh --cask v1.0.0 > mitto.cask.rb

# Generate both (to files in current directory)
./generate-formula.sh --all v1.0.0
# Creates: mitto.rb, mitto.cask.rb

# Use latest release
./generate-formula.sh --all latest

# Show help
./generate-formula.sh --help
```

---

## Packages Overview

| Package | Type | Platforms | What's Installed |
|---------|------|-----------|------------------|
| `mitto` | Formula | macOS, Linux | CLI binary (`/opt/homebrew/bin/mitto`) |
| `mitto` | Cask | macOS only | macOS app (`/Applications/Mitto.app`) + CLI |

The cask depends on the formula, so installing the cask automatically installs the CLI too.

---

## Troubleshooting

### Common Issues

**"SHA256 mismatch"**
- Release assets may still be uploading. Wait a few minutes and retry.
- Verify checksums: `shasum -a 256 <file>`

**"No bottle available"**
- Normal for custom taps. The formula downloads pre-built binaries anyway.

**"Could not resolve HEAD to a revision"**
- The tap repository needs at least one commit.

**Style check fails**
- Run `brew style --fix <file>` to auto-fix some issues.
- Check the templates match current Homebrew conventions.

### Verifying Installation

```bash
mitto --help                    # CLI works
ls /Applications/Mitto.app      # App installed (cask only)
brew info mitto                 # Formula info
brew info --cask mitto          # Cask info
```

---

## Architecture Notes

The formula uses **pre-built binaries** (not source builds) because:

1. Go build requires CGO on macOS for keychain integration
2. Pre-built binaries ensure consistent behavior
3. Faster installation for users

The formula/cask automatically selects the correct binary:
- **macOS Intel**: `mitto-darwin-amd64.tar.gz`
- **macOS Apple Silicon**: `mitto-darwin-arm64.tar.gz`
- **Linux x86_64**: `mitto-linux-amd64.tar.gz`
- **Linux ARM64**: `mitto-linux-arm64.tar.gz`

---

## Alternative: homebrew-core

Submitting to homebrew-core provides broader visibility but has requirements:

- 500+ GitHub stars or significant usage
- Must build from source
- Formula must build in under 1 hour

For now, a custom tap is recommended.

---

## References

- [Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)
- [Homebrew Cask Cookbook](https://docs.brew.sh/Cask-Cookbook)
- [Taps Documentation](https://docs.brew.sh/Taps)
- [Bottles Documentation](https://docs.brew.sh/Bottles)
