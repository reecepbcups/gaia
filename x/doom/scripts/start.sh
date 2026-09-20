#!/usr/bin/env bash
#
# Boots a single validator chain that runs DOOM at its native tic rate and
# leaves it in the foreground. Re-running wipes the chain and starts a new game.
#
#   ./x/doom/scripts/start.sh
#   gaiad doom web --home ~/.gaia-doom --keyring-backend test
#
set -euo pipefail

DOOM_HOME=${DOOM_HOME:-$HOME/.gaia-doom}
CHAIN_ID=${CHAIN_ID:-doom-1}
DENOM=${DENOM:-stake}
KEYRING="--keyring-backend test --home $DOOM_HOME"

# The block is the game clock, so these two set the game's speed between them:
# the sim runs at TICS_PER_BLOCK tics per block and DOOM wants 35 a second.
# Gaia commits about every 57ms on a laptop with timeout_commit at 28ms, which
# 2 tics a block turns into roughly DOOM's native rate. Chase faster blocks with
# TIMEOUT_COMMIT=0ms TICS_PER_BLOCK=1 and check the blocktime readout: land
# under 35 blocks a second that way and the game runs in slow motion.
TICS_PER_BLOCK=${TICS_PER_BLOCK:-2}
TIMEOUT_COMMIT=${TIMEOUT_COMMIT:-28ms}

# Freedoom Phase 1, a BSD-licensed IWAD that plays as Doom 1. The shareware
# DOOM1.WAD isn't ours to hand out, so the demo defaults to this instead.
# Point WAD_URL/WAD_SHA256 at your own IWAD to play the real thing.
WAD_URL=${WAD_URL:-https://github.com/freedoom/freedoom/releases/download/v0.13.0/freedoom-0.13.0.zip}
WAD_SHA256=${WAD_SHA256:-7323bcc168c5a45ff10749b339960e98314740a734c30d4b9f3337001f9e703d}
# Only used when WAD_URL is a zip: which entry inside it is the IWAD.
WAD_ZIP_ENTRY=${WAD_ZIP_ENTRY:-freedoom-0.13.0/freedoom1.wad}

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
GAIAD=${GAIAD:-$ROOT/build/gaiad}

if [ ! -x "$GAIAD" ]; then
  echo "building gaiad"
  make -C "$ROOT" build
fi

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

# The WAD is node-local: the chain only commits to its hash.
WAD_CACHE=${WAD_CACHE:-$HOME/.cache/gaia-doom-iwad.wad}
if [ ! -f "$WAD_CACHE" ]; then
  echo "fetching iwad"
  mkdir -p "$(dirname "$WAD_CACHE")"
  case "$WAD_URL" in
  *.zip)
    command -v unzip >/dev/null || { echo "unzip is needed to unpack $WAD_URL" >&2; exit 1; }
    TMPZIP=$(mktemp -d)
    curl -fsSL -o "$TMPZIP/iwad.zip" "$WAD_URL"
    unzip -p "$TMPZIP/iwad.zip" "$WAD_ZIP_ENTRY" > "$WAD_CACHE" || { rm -f "$WAD_CACHE"; rm -rf "$TMPZIP"; exit 1; }
    rm -rf "$TMPZIP"
    ;;
  *)
    curl -fsSL -o "$WAD_CACHE" "$WAD_URL"
    ;;
  esac
fi

GOT=$(sha256 "$WAD_CACHE")
if [ "$GOT" != "$WAD_SHA256" ]; then
  echo "wad sha256 mismatch: got $GOT, want $WAD_SHA256" >&2
  exit 1
fi

echo "wiping $DOOM_HOME"
rm -rf "$DOOM_HOME"

$GAIAD init doomnode --chain-id "$CHAIN_ID" --home "$DOOM_HOME" >/dev/null 2>&1
cp "$WAD_CACHE" "$DOOM_HOME/doom.wad"

$GAIAD keys add validator $KEYRING >/dev/null 2>&1
$GAIAD keys add player $KEYRING >/dev/null 2>&1
# Only spends when the node is pushing frames into block data. It needs its own
# account so its sequence does not race the browser's.
$GAIAD keys add frames $KEYRING >/dev/null 2>&1

VALIDATOR=$($GAIAD keys show validator -a $KEYRING)
PLAYER=$($GAIAD keys show player -a $KEYRING)
FRAMES=$($GAIAD keys show frames -a $KEYRING)

$GAIAD genesis add-genesis-account "$VALIDATOR" "1000000000000$DENOM" --home "$DOOM_HOME"
$GAIAD genesis add-genesis-account "$PLAYER" "1000000000000$DENOM" --home "$DOOM_HOME"
$GAIAD genesis add-genesis-account "$FRAMES" "1000000000000$DENOM" --home "$DOOM_HOME"
$GAIAD genesis gentx validator "1000000000$DENOM" --chain-id "$CHAIN_ID" $KEYRING >/dev/null 2>&1
$GAIAD genesis collect-gentxs --home "$DOOM_HOME" >/dev/null 2>&1

python3 - "$DOOM_HOME/config/genesis.json" "$WAD_SHA256" "$TICS_PER_BLOCK" <<'PY'
import json, sys

path, wad, tics = sys.argv[1], sys.argv[2], int(sys.argv[3])
with open(path) as f:
    g = json.load(f)

g['app_state']['doom']['params']['wad_hash'] = wad
g['app_state']['doom']['params']['tics_per_block'] = tics

# Playing costs 35 transactions a second, so the fee has to round down to
# nothing. feemarket refuses a zero base price, so use the smallest one it will
# take: 200000 gas then comes to 1 unit of the fee denom per tic.
fm = g['app_state']['feemarket']
fm['params']['min_base_gas_price'] = '0.000000000000000001'
fm['state']['base_gas_price'] = '0.000000000000000001'

with open(path, 'w') as f:
    json.dump(g, f, indent=2)
PY

# DOOM runs at 35 tics a second, so one tic per block wants a 28ms block.
python3 - "$DOOM_HOME/config/config.toml" "$TIMEOUT_COMMIT" <<'PY'
import re, sys

path, timeout_commit = sys.argv[1], sys.argv[2]
s = open(path).read()

for key, value in [
    ('timeout_propose', '80ms'),
    ('timeout_propose_delta', '80ms'),
    ('timeout_prevote', '40ms'),
    ('timeout_prevote_delta', '40ms'),
    ('timeout_precommit', '40ms'),
    ('timeout_precommit_delta', '40ms'),
    ('timeout_commit', timeout_commit),
    ('create_empty_blocks_interval', '0s'),
    # The browser client looks its own transactions back up by hash to show them
    # decoded, so the transaction index has to stay on.
    ('indexer', 'kv'),
]:
    s = re.sub(rf'(?m)^{key} = ".*"$', f'{key} = "{value}"', s)

s = re.sub(r'(?m)^size = 5000$', 'size = 20000', s)
s = re.sub(r'(?m)^discard_abci_responses = .*$', 'discard_abci_responses = true', s)

open(path, 'w').write(s)
PY

python3 - "$DOOM_HOME/config/app.toml" "$DENOM" <<'PY'
import re, sys

path, denom = sys.argv[1], sys.argv[2]
s = open(path).read()
s = re.sub(r'(?m)^minimum-gas-prices = ".*"$', f'minimum-gas-prices = "0{denom}"', s)
# The game is the only state worth keeping and it rebuilds from the input log.
# 'everything' pruning rules out state sync snapshots, which this chain has no
# use for anyway.
s = re.sub(r'(?m)^pruning = ".*"$', 'pruning = "everything"', s)
s = re.sub(r'(?m)^snapshot-interval = .*$', 'snapshot-interval = 0', s)
open(path, 'w').write(s)
PY

cat <<EOF

chain     $CHAIN_ID
home      $DOOM_HOME
wad       $WAD_SHA256
player    $PLAYER
frames    $FRAMES

in another shell:
  $GAIAD doom web --home $DOOM_HOME --keyring-backend test

or, to watch the game entirely out of block data:
  $GAIAD doom web --home $DOOM_HOME --keyring-backend test --frames block

EOF

exec $GAIAD start --home "$DOOM_HOME"
