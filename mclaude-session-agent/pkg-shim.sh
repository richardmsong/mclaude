#!/bin/bash
# pkg shim: translates apt/brew-style package commands to nix profile commands.
# Symlinked as /usr/local/bin/apt and /usr/local/bin/brew as well.
set -e

case "$1" in
  install)
    shift
    for p in "$@"; do
      nix profile install "nixpkgs#$p"
    done
    ;;
  remove|uninstall)
    shift
    for p in "$@"; do
      nix profile remove "$p" 2>/dev/null || true
    done
    ;;
  update|upgrade)
    nix profile upgrade '.*'
    ;;
  list)
    nix profile list
    ;;
  search)
    shift
    nix search nixpkgs "$@"
    ;;
  *)
    echo "Usage: pkg {install|remove|update|list|search} [packages...]" >&2
    exit 1
    ;;
esac
