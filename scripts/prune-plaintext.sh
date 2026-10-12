#!/usr/bin/env bash
#
# prune-plaintext.sh - delete plaintext files that already have an encrypted
# raw envelope (<name>.sops.json) beside them.
#
# `cfgctl encrypt` wraps raw files (.txt/.pem/.key) into a sibling
# "<name>.sops.json" envelope while leaving the original plaintext on disk. Once
# the envelope exists, the plaintext is redundant and should not stay in the
# clear. This script removes exactly those files.
#
# Files encrypted in place (structured .yaml/.yml/.json) are never touched, and
# plaintext files without an envelope are left alone.
#
# Usage:
#   scripts/prune-plaintext.sh <dir> [-n|--dry-run]
#
set -euo pipefail

usage() {
    echo "usage: $0 <dir> [-n|--dry-run]" >&2
    exit 2
}

dir=""
dry_run=false

for arg in "$@"; do
    case "$arg" in
        -n|--dry-run) dry_run=true ;;
        -h|--help) usage ;;
        -*) echo "unknown option: $arg" >&2; usage ;;
        *)
            if [[ -n "$dir" ]]; then
                usage
            fi
            dir="$arg"
            ;;
    esac
done

if [[ -z "$dir" ]]; then
    usage
fi

if [[ ! -d "$dir" ]]; then
    echo "error: not a directory: $dir" >&2
    exit 1
fi

removed=0

while IFS= read -r -d '' file; do
    if [[ -f "$file.sops.json" ]]; then
        if [[ "$dry_run" == true ]]; then
            echo "would remove $file"
        else
            echo "removing $file"
            rm -f -- "$file"
        fi
        removed=$((removed + 1))
    fi
done < <(find "$dir" -type f ! -name '*.sops.json' -print0)

if [[ "$dry_run" == true ]]; then
    echo "$removed plaintext file(s) would be removed."
else
    echo "$removed plaintext file(s) removed."
fi
