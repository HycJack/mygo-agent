#!/bin/bash
# Import rules from spec/architecture.md. A rule break is a spec break.
set -e
cd "$(dirname "$0")/.."
fail() { echo "LAYER VIOLATION: $1" >&2; exit 1; }
bail() { echo "LAYER CHECK ERROR: $1" >&2; exit 1; }

# MOD is the module path, so the script does not hardcode "mygo-agent"
# and keeps working if the module is renamed.
MOD=$(go list -m 2>/dev/null) || bail "not inside a module"

# Every package this check reasons about, discovered rather than listed by
# hand: a directory rename must not silently reduce coverage to nothing.
# Recursive, and a directory with no Go files in it is not a package.
harness_pkgs=("./internal/harness")
while IFS= read -r d; do [ -n "$d" ] && harness_pkgs+=("$d"); done \
  < <(go list ./internal/harness/... 2>/dev/null | sed "s|^$MOD/|./|")
[ "${#harness_pkgs[@]}" -gt 1 ] || bail "no harness subpackages found under ./internal/harness"

provider_pkgs=()
while IFS= read -r p; do [ -n "$p" ] && provider_pkgs+=("$p"); done \
  < <(go list ./internal/providers/... 2>/dev/null | sed "s|^$MOD/|./|")
[ "${#provider_pkgs[@]}" -gt 0 ] || bail "no provider packages found under ./internal/providers"

ui_pkgs=("./internal/ui")
[ -d ./internal/ui ] || bail "./internal/ui is missing"

# The tree must compile before its imports mean anything. `go list -deps`
# is NOT that check: it exits 0 on a file that does not parse, so a
# syntax error would leave every dependency list short and this script
# would cheerfully report success on a tree it never read. `go build` is
# the honest gate, and it warms the cache for the lists below. -test is
# included so a violation hidden in a _test.go file is visible too.
if ! err=$(go build ./... 2>&1 >/dev/null); then
  bail "the tree does not build, so its imports cannot be checked:
$err"
fi
if ! err=$(go vet ./... 2>&1 >/dev/null); then
  bail "go vet failed, so test-only imports cannot be checked:
$err"
fi

# deps lists the in-repo packages a target pulls in, test files included.
# Validity is established above, so a miss here would be a genuine absence
# — but a package that cannot be listed is still an error, not an empty
# answer, so the failure is surfaced instead of swallowed.
deps() {
  local out
  if ! out=$(go list -deps -test "$1" 2>&1 >/dev/null); then
    bail "go list -deps -test $1 failed:
$out"
  fi
  go list -deps -test "$1" 2>/dev/null | grep "^$MOD/" || true
}

# A harness may not reach the host, the UI, or a provider: the host owns
# state, the UI owns rendering, and a harness knows the Sandbox protocol
# rather than an implementation of it.
forbidden_to_harness() {
  case "$1" in
    "$MOD"/internal/app|"$MOD"/internal/ui|"$MOD"/internal/providers|"$MOD"/internal/providers/*) return 0 ;;
    *) return 1 ;;
  esac
}

forbidden_to_ui() {
  case "$1" in
    "$MOD"/internal/app|"$MOD"/internal/providers|"$MOD"/internal/providers/*) return 0 ;;
    *) return 1 ;;
  esac
}

for pkg in "${harness_pkgs[@]}"; do
  for dep in $(deps "$pkg"); do
    if forbidden_to_harness "$dep"; then
      fail "$dep imported by ${pkg#./}"
    fi
  done
done

# internal/ui renders from ViewModels and reports through Actions: it may
# use the harness value types and the toolkit, never the host or a provider.
for pkg in "${ui_pkgs[@]}"; do
  for dep in $(deps "$pkg"); do
    if forbidden_to_ui "$dep"; then
      fail "$dep imported by ${pkg#./}"
    fi
  done
done

# Providers supply isolation and memory to the harness protocol and to
# nothing else in the repo.
for pkg in "${provider_pkgs[@]}"; do
  mod="$MOD/${pkg#./}" # go list reports module paths, the caller gave ./dir
  for dep in $(deps "$pkg"); do
    # $mod and $mod.test are the package itself; -test adds the latter.
    case "$dep" in
      "$mod"|"$mod".test) ;;
      "$MOD"/internal/app|"$MOD"/internal/ui|"$MOD"/internal/providers|"$MOD"/internal/providers/*)
        fail "$dep imported by $mod"
        ;;
    esac
  done
done

# The protocol root is the one package everything may name, and it may
# name nothing of ours but the shared cli helper.
for dep in $(deps ./internal/harness); do
  case "$dep" in
    "$MOD"/internal/harness|"$MOD"/internal/harness.test|"$MOD"/internal/harness/cli|"$MOD"/internal/harness/cli.test) ;;
    "$MOD"/internal/*) fail "$dep imported by internal/harness (the protocol root names only itself and cli)" ;;
  esac
done

echo "layers ok ($(( ${#harness_pkgs[@]} - 1 )) harness subpackages, ${#provider_pkgs[@]} provider, ${#ui_pkgs[@]} ui)"
