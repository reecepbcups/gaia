#!/usr/bin/env bash
#
# Boots a single validator chain holding a Minecraft world and leaves it in the
# foreground. Re-running wipes the chain and starts a new world.
#
#   ./x/mc/scripts/start.sh
#   gaiad mc serve --home ~/.gaia-mc --keyring-backend test
#
# Then add a server in the client pointing at 127.0.0.1:25565.
set -euo pipefail

MC_HOME=${MC_HOME:-$HOME/.gaia-mc}
CHAIN_ID=${CHAIN_ID:-mc-1}
DENOM=${DENOM:-stake}
KEYRING="--keyring-backend test --home $MC_HOME"

# The block is the world clock, so this and the block rate set the world's
# speed between them. Minecraft wants 20 ticks a second and gaia commits about
# 20 blocks a second with the transaction indexer off, so one tick a block
# lands about right. Nothing the client draws depends on it; a day cycle would.
TICKS_PER_BLOCK=${TICKS_PER_BLOCK:-1}
TIMEOUT_COMMIT=${TIMEOUT_COMMIT:-0ms}
GROUND_LEVEL=${GROUND_LEVEL:-64}

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
GAIAD=${GAIAD:-$ROOT/build/gaiad}

if [ ! -x "$GAIAD" ]; then
  echo "building gaiad"
  make -C "$ROOT" build
fi

echo "wiping $MC_HOME"
rm -rf "$MC_HOME"

$GAIAD init mcnode --chain-id "$CHAIN_ID" --home "$MC_HOME" >/dev/null 2>&1

$GAIAD keys add validator $KEYRING >/dev/null 2>&1
$GAIAD keys add player $KEYRING >/dev/null 2>&1

VALIDATOR=$($GAIAD keys show validator -a $KEYRING)
PLAYER=$($GAIAD keys show player -a $KEYRING)

$GAIAD genesis add-genesis-account "$VALIDATOR" "1000000000000$DENOM" --home "$MC_HOME"
$GAIAD genesis add-genesis-account "$PLAYER" "1000000000000$DENOM" --home "$MC_HOME"
$GAIAD genesis gentx validator "1000000000$DENOM" --chain-id "$CHAIN_ID" $KEYRING >/dev/null 2>&1
$GAIAD genesis collect-gentxs --home "$MC_HOME" >/dev/null 2>&1

python3 - "$MC_HOME/config/genesis.json" "$TICKS_PER_BLOCK" "$GROUND_LEVEL" <<'PY'
import json, sys

path, ticks, ground = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
with open(path) as f:
    g = json.load(f)

g['app_state']['mc']['params']['ticks_per_block'] = ticks
g['app_state']['mc']['params']['ground_level'] = ground

# Playing costs a transaction a block, so the fee has to round down to nothing.
# feemarket refuses a zero base price, so use the smallest one it will take.
fm = g['app_state']['feemarket']
fm['params']['min_base_gas_price'] = '0.000000000000000001'
fm['state']['base_gas_price'] = '0.000000000000000001'

with open(path, 'w') as f:
    json.dump(g, f, indent=2)
PY

python3 - "$MC_HOME/config/config.toml" "$TIMEOUT_COMMIT" <<'PY'
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
    # Nothing here looks a transaction up by hash, and indexing every block's
    # worth of play costs about 9ms a block, which is most of the gap between
    # the block rate and Minecraft's 20 ticks a second.
    ('indexer', 'null'),
]:
    s = re.sub(rf'(?m)^{key} = ".*"$', f'{key} = "{value}"', s)

s = re.sub(r'(?m)^size = 5000$', 'size = 20000', s)
# The explorer reads transaction results out of /block_results, so the node has
# to keep them. Throwing them away saves a little disk and loses the only thing
# that says whether a transaction was accepted.

open(path, 'w').write(s)
PY

python3 - "$MC_HOME/config/app.toml" "$DENOM" <<'PY'
import re, sys

path, denom = sys.argv[1], sys.argv[2]
s = open(path).read()
s = re.sub(r'(?m)^minimum-gas-prices = ".*"$', f'minimum-gas-prices = "0{denom}"', s)
# The world rebuilds from the input log, so old state is not worth keeping.
s = re.sub(r'(?m)^pruning = ".*"$', 'pruning = "everything"', s)
s = re.sub(r'(?m)^snapshot-interval = .*$', 'snapshot-interval = 0', s)
open(path, 'w').write(s)
PY

cat <<EOF

chain     $CHAIN_ID
home      $MC_HOME
player    $PLAYER
ground    y=$GROUND_LEVEL

in another shell:
  $GAIAD mc serve --home $MC_HOME --keyring-backend test

then point your client at 127.0.0.1:25565

EOF

exec $GAIAD start --home "$MC_HOME"
