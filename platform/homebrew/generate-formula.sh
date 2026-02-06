#!/bin/bash
#
# Generate Homebrew formula and/or cask from templates using a specific release version.
#
# Usage:
#   ./generate-formula.sh [--formula|--cask|--all] <version|latest>
#
# Options:
#   --formula    Generate only the CLI formula (default)
#   --cask       Generate only the macOS app cask
#   --all        Generate both formula and cask (outputs to separate files)
#
# Examples:
#   ./generate-formula.sh v1.2.3              # Generate formula to stdout
#   ./generate-formula.sh --cask v1.2.3       # Generate cask to stdout
#   ./generate-formula.sh --all v1.2.3        # Generate both to mitto.rb and mitto.cask.rb
#   ./generate-formula.sh latest              # Use latest release

set -euo pipefail

REPO="inercia/mitto"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORMULA_TEMPLATE="${SCRIPT_DIR}/mitto.rb.template"
CASK_TEMPLATE="${SCRIPT_DIR}/mitto.rb.cask.template"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

usage() {
    echo "Usage: $0 [--formula|--cask|--all] <version|latest>"
    echo ""
    echo "Options:"
    echo "  --formula    Generate only the CLI formula (default)"
    echo "  --cask       Generate only the macOS app cask"
    echo "  --all        Generate both formula and cask"
    echo ""
    echo "Arguments:"
    echo "  version    Release version (e.g., v1.2.3)"
    echo "  latest     Use the latest release"
    echo ""
    echo "Examples:"
    echo "  $0 v1.2.3                    # Generate formula to stdout"
    echo "  $0 --cask v1.2.3             # Generate cask to stdout"
    echo "  $0 --all v1.2.3              # Generate both to mitto.rb and mitto.cask.rb"
    exit 1
}

# Check dependencies
check_deps() {
    for cmd in curl jq sha256sum; do
        if ! command -v "$cmd" &> /dev/null; then
            # On macOS, sha256sum might be shasum
            if [[ "$cmd" == "sha256sum" ]] && command -v shasum &> /dev/null; then
                continue
            fi
            log_error "Required command '$cmd' not found"
            exit 1
        fi
    done
}

# Calculate SHA256 (cross-platform)
calc_sha256() {
    local file="$1"
    if command -v sha256sum &> /dev/null; then
        sha256sum "$file" | cut -d' ' -f1
    else
        shasum -a 256 "$file" | cut -d' ' -f1
    fi
}

# Get latest release version from GitHub
get_latest_version() {
    curl -s "https://api.github.com/repos/${REPO}/releases/latest" | jq -r '.tag_name'
}

# Download file and get SHA256
get_sha256() {
    local url="$1"
    local tmpfile
    tmpfile=$(mktemp)
    trap "rm -f $tmpfile" RETURN
    
    log_info "Downloading: $url"
    if curl -sL --fail "$url" -o "$tmpfile"; then
        calc_sha256 "$tmpfile"
    else
        log_error "Failed to download: $url"
        echo ""
    fi
}

# Generate formula from template
generate_formula() {
    local version_clean="$1"
    local base_url="$2"
    local sha256_amd64="$3"
    local sha256_arm64="$4"
    local sha256_linux_amd64="$5"
    local sha256_linux_arm64="$6"

    local url_amd64="${base_url}/mitto-darwin-amd64.tar.gz"
    local url_arm64="${base_url}/mitto-darwin-arm64.tar.gz"

    sed -e "s|{{VERSION}}|${version_clean}|g" \
        -e "s|{{SHA256_AMD64}}|${sha256_amd64}|g" \
        -e "s|{{SHA256_ARM64}}|${sha256_arm64}|g" \
        -e "s|{{SHA256_LINUX_AMD64}}|${sha256_linux_amd64}|g" \
        -e "s|{{SHA256_LINUX_ARM64}}|${sha256_linux_arm64}|g" \
        -e "s|{{URL_AMD64}}|${url_amd64}|g" \
        -e "s|{{URL_ARM64}}|${url_arm64}|g" \
        "$FORMULA_TEMPLATE"
}

# Generate cask from template
generate_cask() {
    local version_clean="$1"
    local sha256_app_amd64="$2"
    local sha256_app_arm64="$3"

    sed -e "s|{{VERSION}}|${version_clean}|g" \
        -e "s|{{SHA256_APP_AMD64}}|${sha256_app_amd64}|g" \
        -e "s|{{SHA256_APP_ARM64}}|${sha256_app_arm64}|g" \
        "$CASK_TEMPLATE"
}

# Main
main() {
    local mode="formula"
    local version=""

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --formula)
                mode="formula"
                shift
                ;;
            --cask)
                mode="cask"
                shift
                ;;
            --all)
                mode="all"
                shift
                ;;
            -h|--help)
                usage
                ;;
            *)
                version="$1"
                shift
                ;;
        esac
    done

    if [[ -z "$version" ]]; then
        usage
    fi

    check_deps

    # Get latest version if requested
    if [[ "$version" == "latest" ]]; then
        version=$(get_latest_version)
        if [[ -z "$version" || "$version" == "null" ]]; then
            log_error "Failed to get latest version from GitHub"
            exit 1
        fi
        log_info "Latest version: $version"
    fi

    # Strip 'v' prefix for formula version
    local version_clean="${version#v}"
    local base_url="https://github.com/${REPO}/releases/download/${version}"

    # Get SHA256 checksums based on mode
    log_info "Calculating SHA256 checksums..."

    local sha256_amd64="" sha256_arm64="" sha256_linux_amd64="" sha256_linux_arm64=""
    local sha256_app_amd64="" sha256_app_arm64=""

    if [[ "$mode" == "formula" || "$mode" == "all" ]]; then
        sha256_amd64=$(get_sha256 "${base_url}/mitto-darwin-amd64.tar.gz")
        sha256_arm64=$(get_sha256 "${base_url}/mitto-darwin-arm64.tar.gz")
        sha256_linux_amd64=$(get_sha256 "${base_url}/mitto-linux-amd64.tar.gz")
        sha256_linux_arm64=$(get_sha256 "${base_url}/mitto-linux-arm64.tar.gz")

        if [[ -z "$sha256_amd64" || -z "$sha256_arm64" ]]; then
            log_error "Failed to get SHA256 for CLI binaries"
            exit 1
        fi
    fi

    if [[ "$mode" == "cask" || "$mode" == "all" ]]; then
        sha256_app_amd64=$(get_sha256 "${base_url}/Mitto-darwin-amd64.zip")
        sha256_app_arm64=$(get_sha256 "${base_url}/Mitto-darwin-arm64.zip")

        if [[ -z "$sha256_app_amd64" || -z "$sha256_app_arm64" ]]; then
            log_error "Failed to get SHA256 for macOS app bundles"
            exit 1
        fi
    fi

    # Generate output based on mode
    case "$mode" in
        formula)
            log_info "Generating formula..."
            generate_formula "$version_clean" "$base_url" "$sha256_amd64" "$sha256_arm64" "$sha256_linux_amd64" "$sha256_linux_arm64"
            log_info "Formula generated successfully!"
            ;;
        cask)
            log_info "Generating cask..."
            generate_cask "$version_clean" "$sha256_app_amd64" "$sha256_app_arm64"
            log_info "Cask generated successfully!"
            ;;
        all)
            log_info "Generating formula to mitto.rb..."
            generate_formula "$version_clean" "$base_url" "$sha256_amd64" "$sha256_arm64" "$sha256_linux_amd64" "$sha256_linux_arm64" > mitto.rb
            log_info "Generating cask to mitto.cask.rb..."
            generate_cask "$version_clean" "$sha256_app_amd64" "$sha256_app_arm64" > mitto.cask.rb
            log_info "Both formula and cask generated successfully!"
            log_info "Files created: mitto.rb, mitto.cask.rb"
            ;;
    esac
}

main "$@"

