#!/usr/bin/env bash
# Sign SBUILD_LIST.json files with minisign for release
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
KEYS_DIR="$REPO_ROOT/keys"

# Check if minisign is installed
if ! command -v minisign &> /dev/null; then
    echo "Error: minisign is not installed"
    echo "Install with: sudo apt install minisign (Debian/Ubuntu)"
    echo "           or: brew install minisign (macOS)"
    exit 1
fi

# Function to sign a file (requires tmp_key to be set by caller)
sign_file() {
    local file="$1"
    local tmp_key="$2"

    if [ ! -f "$file" ]; then
        echo "Error: File not found: $file"
        return 1
    fi

    echo "Signing: $file"

    # Sign with temporary key
    # -S = sign
    # -s = secret key file
    # -m = message file
    # -x = signature output file (use .sig extension)
    local sig_file="${file}.sig"

    # If password is provided, use printf to pipe it (works for each iteration)
    if [ -n "${MINISIGN_PASSWORD:-}" ]; then
        printf '%s\n' "$MINISIGN_PASSWORD" | minisign -S -s "$tmp_key" -m "$file" -x "$sig_file"
    else
        minisign -S -s "$tmp_key" -m "$file" -x "$sig_file"
    fi

    echo "  ✓ Created: ${file}.sig"
}

# Main execution
main() {
    echo "==================================================="
    echo "Signing SBUILD_LIST.json files with minisign"
    echo "==================================================="
    echo ""

    # Check for files to sign
    local files_to_sign=()

    if [ $# -eq 0 ]; then
        # No arguments - look for SBUILD_LIST.json files
        echo "Looking for SBUILD_LIST.json files..."

        # Check common locations
        for pattern in \
            "$REPO_ROOT/bincache-SBUILD_LIST.json" \
            "$REPO_ROOT/pkgcache-SBUILD_LIST.json" \
            "$REPO_ROOT/artifacts/bincache-SBUILD_LIST.json" \
            "$REPO_ROOT/artifacts/pkgcache-SBUILD_LIST.json"; do

            if [ -f "$pattern" ]; then
                files_to_sign+=("$pattern")
            fi
        done

        if [ ${#files_to_sign[@]} -eq 0 ]; then
            echo "Error: No SBUILD_LIST.json files found"
            echo ""
            echo "Usage: $0 [file1.json file2.json ...]"
            echo ""
            echo "Or place files at:"
            echo "  - bincache-SBUILD_LIST.json"
            echo "  - pkgcache-SBUILD_LIST.json"
            exit 1
        fi
    else
        # Use provided files
        files_to_sign=("$@")
    fi

    # Check if private key is in environment variable
    if [ -z "${MINISIGN_KEY_CONTENT:-}" ]; then
        echo "Error: MINISIGN_KEY_CONTENT environment variable not set!"
        echo ""
        echo "Set the private key content:"
        echo "  export MINISIGN_KEY_CONTENT=\$(cat keys/minisign.key)"
        echo ""
        echo "Or in CI/CD, use GitHub Secret: MINISIGN_PRIVATE_KEY"
        exit 1
    fi

    # Create temporary key file from environment variable (once for all files)
    local tmp_key=$(mktemp)
    trap "rm -f $tmp_key" EXIT

    echo "$MINISIGN_KEY_CONTENT" > "$tmp_key"
    chmod 600 "$tmp_key"

    # Sign all files
    local signed_count=0
    echo "Total files to sign: ${#files_to_sign[@]}"
    for file in "${files_to_sign[@]}"; do
        echo ""
        echo "Processing file $((signed_count + 1))/${#files_to_sign[@]}: $file"
        if sign_file "$file" "$tmp_key"; then
            ((signed_count++))
        else
            echo "  ✗ Failed to sign: $file"
        fi
    done

    echo ""
    echo "==================================================="
    echo "Signed $signed_count file(s)"
    echo "==================================================="
    echo ""
    echo "Upload these files to release assets:"
    for file in "${files_to_sign[@]}"; do
        echo "  - $(basename "$file")"
        echo "  - $(basename "$file").minisig"
    done
    echo ""
    echo "Example GitHub Release command:"
    echo "  gh release create v1.0.0 \\"
    for file in "${files_to_sign[@]}"; do
        echo "    \"$file\" \\"
        echo "    \"${file}.minisig\" \\"
    done
    echo ""
}

main "$@"
