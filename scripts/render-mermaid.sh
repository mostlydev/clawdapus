#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$REPO_ROOT/docs/art/mermaid"
mkdir -p "$OUT_DIR"

# Scoped markdown files
SCOPE_GLOBS=(
  "README.md"
  "cllama-passthrough/README.md"
  "docs/**/*.md"
)

# Collect matching files
files=()
for glob in "${SCOPE_GLOBS[@]}"; do
  while IFS= read -r f; do
    [[ -f "$f" ]] && files+=("$f")
  done < <(cd "$REPO_ROOT" && eval "ls -1 $glob 2>/dev/null" || true)
done

if [[ ${#files[@]} -eq 0 ]]; then
  echo "No markdown files in scope."
  exit 0
fi

changed=()

for file in "${files[@]}"; do
  filepath="$REPO_ROOT/$file"
  # Derive name stem: README.md -> readme, cllama-passthrough/README.md -> cllama-passthrough-readme
  stem=$(echo "$file" | sed 's|/|-|g; s|\.md$||' | tr '[:upper:]' '[:lower:]')

  # Extract mermaid blocks with line numbers
  block_index=0
  in_block=false
  block_content=""
  block_end_lines=()
  block_contents=()

  while IFS= read -r line_num_and_content; do
    line_num="${line_num_and_content%%:*}"
    content="${line_num_and_content#*:}"

    if [[ "$in_block" == false ]] && [[ "$content" == '```mermaid' ]]; then
      in_block=true
      block_content=""
      continue
    fi

    if [[ "$in_block" == true ]]; then
      if [[ "$content" == '```' ]]; then
        in_block=false
        block_index=$((block_index + 1))
        block_end_lines+=("$line_num")
        block_contents+=("$block_content")
      else
        block_content+="$content"$'\n'
      fi
    fi
  done < <(grep -n '' "$filepath")

  if [[ $block_index -eq 0 ]]; then
    continue
  fi

  # Render each block to SVG
  for i in $(seq 0 $((block_index - 1))); do
    idx=$((i + 1))
    svg_name="${stem}-${idx}.svg"
    svg_path="$OUT_DIR/$svg_name"
    tmp_mmd=$(mktemp /tmp/mermaid-XXXXXX.mmd)
    echo "${block_contents[$i]}" > "$tmp_mmd"

    echo "Rendering $svg_name ..."
    npx -y @mermaid-js/mermaid-cli -i "$tmp_mmd" -o "$svg_path" -b transparent --quiet 2>/dev/null || {
      echo "  WARNING: failed to render $svg_name" >&2
      rm -f "$tmp_mmd"
      continue
    }
    rm -f "$tmp_mmd"
    changed+=("$svg_path")
  done

  # Inject/update image references in the markdown file
  # Work backwards so line numbers stay valid
  tmp_md=$(mktemp /tmp/md-XXXXXX.md)
  cp "$filepath" "$tmp_md"

  for i in $(seq $((block_index - 1)) -1 0); do
    idx=$((i + 1))
    svg_name="${stem}-${idx}.svg"
    end_line="${block_end_lines[$i]}"

    # Compute relative path from the markdown file to the SVG
    file_dir=$(dirname "$file")
    if [[ "$file_dir" == "." ]]; then
      rel_path="docs/art/mermaid/$svg_name"
    else
      # Compute relative path from file_dir to docs/art/mermaid
      rel_path=$(python3 -c "import os.path; print(os.path.relpath('docs/art/mermaid/$svg_name', '$file_dir'))")
    fi

    img_ref="![${stem}-${idx}]($rel_path)"

    # Check if the line after the closing ``` already has an image ref
    next_line=$((end_line + 1))
    next_content=$(sed -n "${next_line}p" "$tmp_md" 2>/dev/null || echo "")

    if [[ "$next_content" == '!['*']('*'.svg)' ]]; then
      # Update existing image reference
      sed -i '' "${next_line}s|.*|${img_ref}|" "$tmp_md"
    else
      # Insert image reference after the closing ```
      sed -i '' "${end_line}a\\
${img_ref}" "$tmp_md"
    fi
  done

  if ! diff -q "$filepath" "$tmp_md" >/dev/null 2>&1; then
    cp "$tmp_md" "$filepath"
    changed+=("$filepath")
  fi
  rm -f "$tmp_md"
done

if [[ ${#changed[@]} -gt 0 ]]; then
  echo ""
  echo "Updated files:"
  for f in "${changed[@]}"; do
    echo "  $f"
  done
fi
