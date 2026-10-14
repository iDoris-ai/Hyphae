#!/usr/bin/env bash
# Paired Linux repro for the standalone DM positive control. The SAME test file
# is overlaid on git-archive trees of baseline 127f460 and candidate 47d8daf.
# Production code untouched. Usage (from repo root):
#   bash baton-proofgap/dmcontrol/repro.sh <out_dir>
set -u
OUT=${1:-/tmp/dmctl-out}; WORK=$(mktemp -d); mkdir -p "$OUT"
F=internal/messaging/proofgap_dm_control_linux_test.go
RUN='^TestProofGapDMPositiveControl$'
PKG=./internal/messaging/
echo "overlay $F sha256=$(sha256sum "$F" | cut -d' ' -f1)" | tee "$OUT/source_hash.txt"
for pair in b127:127f460c0eacc2e2d23067fdba485c636cb7dd0f c47:47d8daf824a5dcee2bfb6488da3b62ce75594aa2; do
  name=${pair%%:*}; sha=${pair#*:}; dir=$WORK/$name; mkdir -p "$dir"
  git archive "$sha" | tar -x -C "$dir"
  test -e "$dir/$F" && echo "$name: $F already exists in archive (unexpected)" | tee -a "$OUT/exit_codes.txt"
  cp "$F" "$dir/$F"
  ( cd "$dir"
    echo "$name tree=$sha overlay_sha256=$(sha256sum "$F" | cut -d' ' -f1)" | tee -a "$OUT/source_hash.txt"
    go vet $PKG >"$OUT/$name.dm.vet.log" 2>&1; echo "$name vet exit=$?" | tee -a "$OUT/exit_codes.txt"
    for i in 1 2 3; do
      go test -race -count=1 -json -timeout 5m -run "$RUN" $PKG >"$OUT/$name.dm.race$i.json" 2>&1
      echo "$name race$i exit=$?" | tee -a "$OUT/exit_codes.txt"
    done )
done
rm -rf "$WORK"
