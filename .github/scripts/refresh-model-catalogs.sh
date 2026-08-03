#!/usr/bin/env bash
set -euo pipefail

models_repository="${MODELS_REPOSITORY_URL:-https://github.com/router-for-me/models.git}"
models_ref="${MODELS_REPOSITORY_REF:-main}"
catalog_dir="${MODEL_CATALOG_DIR:-internal/registry/models}"
codex_catalog="$catalog_dir/codex_client_models.json"
codex_candidate="$(mktemp)"
trap 'rm -f "$codex_candidate"' EXIT

validate_codex_catalog() {
  (
    unset GOOS GOARCH
    go run ./cmd/validate_codex_models --file "$1"
  )
}

git fetch --depth 1 "$models_repository" "$models_ref"
git show FETCH_HEAD:models.json > "$catalog_dir/models.json"

if git show FETCH_HEAD:codex_client_models.json > "$codex_candidate" && validate_codex_catalog "$codex_candidate"; then
  mv "$codex_candidate" "$codex_catalog"
  printf 'Refreshed validated Codex client model catalog.\n'
else
	printf '::warning::Remote Codex client model catalog is missing or invalid; using embedded fallback.\n'
fi

validate_codex_catalog "$codex_catalog"
