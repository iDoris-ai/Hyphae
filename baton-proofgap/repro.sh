#!/usr/bin/env bash
# Paired Linux repro: same test-only proof-gap files overlaid on git-archive
# trees of baseline 127f460 and candidate 47d8daf. Production code untouched.
# Usage: bash baton-proofgap/repro.sh <out_dir>   (run from repo root)
set -u
OUT=${1:-/tmp/pg-out}; WORK=$(mktemp -d); mkdir -p "$OUT"
FILES="internal/groupchat/proofgap_linux_test.go internal/daemon/proofgap_linux_test.go internal/tui/proofgap_linux_test.go"
RUN='TestProofGap'
PKGS="./internal/groupchat/ ./internal/daemon/ ./internal/tui/"
for pair in b127:127f460c0eacc2e2d23067fdba485c636cb7dd0f c47:47d8daf824a5dcee2bfb6488da3b62ce75594aa2; do
  name=${pair%%:*}; sha=${pair#*:}; dir=$WORK/$name; mkdir -p "$dir"
  git archive "$sha" | tar -x -C "$dir"
  for f in $FILES; do cp "$f" "$dir/$f"; done
  ( cd "$dir"
    go vet $PKGS >"$OUT/$name.vet.log" 2>&1; echo "$name vet exit=$?" | tee -a "$OUT/exit_codes.txt"
    go build -o "$WORK/$name.hyphae" ./cmd/hyphae >"$OUT/$name.build.log" 2>&1; echo "$name build exit=$?" | tee -a "$OUT/exit_codes.txt"
    for i in 1 2 3; do
      go test -race -count=1 -json -timeout 5m -run "$RUN" $PKGS >"$OUT/$name.race$i.json" 2>&1
      echo "$name race$i exit=$?" | tee -a "$OUT/exit_codes.txt"
    done )
done
rm -rf "$WORK"
