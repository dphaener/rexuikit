#!/bin/sh
# Snapshot the running Rex server's own API docs into rexuikit/docs/rex-api/.
#
# Writes llms-r{rev}.md (GET /llms.txt), api-help-r{rev}.json (GET /api/help,
# jq-pretty) and VERSION (rex version, commit, api revision, server built_at).
# VERSION carries no run date, so rerunning against the same server is
# byte-identical. This script may shell out to curl/jq; the no-CLI rule applies
# to rexuikit's Go code only.
#
# Server address: REX_SERVER (a unix:// URL whose path is percent-encoded, e.g.
# "Application%20Support"), else the default macOS socket. Needs an
# unsandboxed run: the sandbox blocks the Rex socket.
set -eu

die() { echo "rex-api-snapshot: $*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl not found"
command -v jq >/dev/null 2>&1 || die "jq not found"

# percent_decode turns %XX escapes into bytes. printf %b is the portable
# decoder; a literal backslash in the input is escaped first so it survives.
percent_decode() {
	printf '%b' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g')"
}

if [ -n "${REX_SERVER:-}" ]; then
	case $REX_SERVER in
	unix://*) sock=$(percent_decode "${REX_SERVER#unix://}") ;;
	*) die "REX_SERVER must be a unix:// URL, got: $REX_SERVER" ;;
	esac
else
	[ -n "${HOME:-}" ] || die "HOME is unset and REX_SERVER is empty"
	sock="$HOME/Library/Application Support/rex/server.sock"
fi

[ -S "$sock" ] || die "no Rex socket at '$sock' (is the Rex server running? is this sandboxed?)"

here=$(cd "$(dirname "$0")" && pwd)
out="$here/../docs/rex-api"
mkdir -p "$out"

get() {
	curl -sS --fail --max-time 10 --unix-socket "$sock" "http://localhost$1"
}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/rex-api-snapshot.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

get /api/help >"$tmp/help.json" || die "GET /api/help failed on '$sock'"
get /llms.txt >"$tmp/llms.md" || die "GET /llms.txt failed on '$sock'"

jq -e . "$tmp/help.json" >/dev/null || die "/api/help did not return JSON"

# server.version is a method; its api_revision is the source of truth. The
# call goes over the control WebSocket, so use the CLI rather than hand-rolling
# framing in shell.
command -v rex >/dev/null 2>&1 || die "rex CLI not found (needed for server.version)"
ver=$(REX_SERVER="unix://$(printf '%s' "$sock" | sed -e 's/%/%25/g' -e 's/ /%20/g')" rex api call server.version) ||
	die "rex api call server.version failed"
rev=$(printf '%s' "$ver" | jq -er '.api_revision') || die "server.version has no api_revision"
case $rev in '' | *[!0-9]*) die "api_revision is not a number: $rev" ;; esac

# Pretty-print with sorted keys so a server that reorders map output cannot
# cause a spurious diff.
jq -S . "$tmp/help.json" >"$out/api-help-r$rev.json"
cp "$tmp/llms.md" "$out/llms-r$rev.md"

{
	printf 'rex_version=%s\n' "$(printf '%s' "$ver" | jq -r '.rex_version')"
	printf 'commit=%s\n' "$(printf '%s' "$ver" | jq -r '.commit')"
	printf 'api_revision=%s\n' "$rev"
	printf 'built_at=%s\n' "$(printf '%s' "$ver" | jq -r '.built_at')"
} >"$out/VERSION"

echo "rex-api-snapshot: wrote r$rev to $out"
