#!/usr/bin/env bash
#
# Generate Markdown release notes from conventional commits between two tags.
# Usage: ./scripts/generate-release-notes.sh <tag>
#   e.g. ./scripts/generate-release-notes.sh v0.1.0
#
# If no previous tag exists, all commits up to <tag> are included.

set -eo pipefail

TAG="${1:?Usage: generate-release-notes.sh <tag>}"

# Find the previous tag. If none exists, use the root commit.
PREV_TAG=$(git describe --tags --abbrev=0 "${TAG}^" 2>/dev/null || true)

if [ -n "$PREV_TAG" ]; then
  RANGE="${PREV_TAG}..${TAG}"
  COMPARE_TEXT="**Full changelog**: \`${PREV_TAG}...${TAG}\`"
else
  RANGE="${TAG}"
  COMPARE_TEXT="**Initial release**"
fi

# Collect commits into temp files by category
TMPDIR_NOTES=$(mktemp -d)
trap 'rm -rf "$TMPDIR_NOTES"' EXIT

for prefix in feat fix refactor perf docs test chore other; do
  : > "${TMPDIR_NOTES}/${prefix}"
done

while IFS= read -r line; do
  [ -z "$line" ] && continue
  MATCHED=false
  for prefix in feat fix refactor perf docs test chore; do
    if [[ "$line" =~ ^${prefix}(\(.*\))?:\ (.+)$ ]]; then
      SCOPE="${BASH_REMATCH[1]}"
      MSG="${BASH_REMATCH[2]}"
      if [ -n "$SCOPE" ]; then
        echo "- **${SCOPE}**: ${MSG}" >> "${TMPDIR_NOTES}/${prefix}"
      else
        echo "- ${MSG}" >> "${TMPDIR_NOTES}/${prefix}"
      fi
      MATCHED=true
      break
    fi
  done
  if [ "$MATCHED" = false ]; then
    echo "- ${line}" >> "${TMPDIR_NOTES}/other"
  fi
done < <(git log --format="%s" "$RANGE" 2>/dev/null)

# Section display names
section_title() {
  case "$1" in
    feat)     echo "Features" ;;
    fix)      echo "Bug Fixes" ;;
    refactor) echo "Refactoring" ;;
    perf)     echo "Performance" ;;
    docs)     echo "Documentation" ;;
    test)     echo "Tests" ;;
    chore)    echo "Chores" ;;
  esac
}

# Build output
echo "# Nexara ${TAG}"
echo ""

for prefix in feat fix refactor perf docs test chore; do
  if [ -s "${TMPDIR_NOTES}/${prefix}" ]; then
    echo "## $(section_title "$prefix")"
    echo ""
    cat "${TMPDIR_NOTES}/${prefix}"
    echo ""
  fi
done

if [ -s "${TMPDIR_NOTES}/other" ]; then
  echo "## Other Changes"
  echo ""
  cat "${TMPDIR_NOTES}/other"
  echo ""
fi

echo "---"
echo ""
echo "${COMPARE_TEXT}"
echo ""
echo "## Container Images"
echo ""
echo "\`\`\`bash"
echo "# Pull all services"
VERSION="${TAG#v}"
echo "docker pull gitea.crjlab.net/bigjakk/nexara-api:${VERSION}"
echo "docker pull gitea.crjlab.net/bigjakk/nexara-ws:${VERSION}"
echo "docker pull gitea.crjlab.net/bigjakk/nexara-collector:${VERSION}"
echo "docker pull gitea.crjlab.net/bigjakk/nexara-scheduler:${VERSION}"
echo "docker pull gitea.crjlab.net/bigjakk/nexara-frontend:${VERSION}"
echo ""
echo "# Or use docker-compose.prod.yml"
echo "NEXARA_VERSION=${VERSION} docker compose -f docker-compose.prod.yml up -d"
echo "\`\`\`"
