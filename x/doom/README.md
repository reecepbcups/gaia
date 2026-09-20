# x/doom

DOOM as a cosmos-sdk module. The game is consensus state, the keyboard is a mempool.

Every tic of the game is advanced by an `EndBlocker`, and the buttons you're holding get there
as a signed transaction. There's no game server anywhere. What you see on screen is whatever
the chain's last block computed.

## Why this works at all

DOOM was built for 1993 modems, which means the netcode is already the shape you want:

- The sim is deterministic. Fixed-point math everywhere, and `M_Random` is a hardcoded 256-byte
  table, not an RNG.
- It runs on a fixed 35 Hz timestep, and the only per-player input is a 5-byte `ticcmd_t`.
- Demos are just recorded ticcmds. Replay the inputs, get the identical game back.

So the chain stores the input log, and the game is a materialized view of it. Any node can
rebuild the whole thing by replaying from tic 0, and each block commits a sha256 of the engine's
memory so a divergent replay shows up as an apphash mismatch instead of a silent fork.

**Blocks are the game clock.** Nothing else advances it. A block runs `tics_per_block` tics in
its `EndBlocker`, so with the default of 2 the game is at tic 2N when the chain is at height N.
If the chain stalls, DOOM freezes mid-frame; if blocks speed up, so does the game. There is no
wall clock anywhere inside the sandbox, which is exactly why the replay comes out identical.

## How it's put together

```text
browser  --signed MsgInput-->  mempool  -->  EndBlocker  -->  wasm DOOM  -->  framebuffer
   ^                                                                              |
   +------------------------ chunked HTTP frame stream ---------------------------+
```

- `wasmbuild/` holds `doomgeneric_chain.c`, a [doomgeneric](https://github.com/ozkl/doomgeneric)
  port whose "platform" is the host that calls it. It can't read a clock, a device or an entropy
  source. Upstream's 178 files of C sit next to it in `doomgeneric.tar.gz` so they stay out of
  every diff. The Makefile unpacks them and builds `wasm32-wasi`.
- `engine/` runs that wasm under [wazero](https://wazero.io) (pure Go, no cgo). The sandbox's
  linear memory *is* the game state, which is what makes hashing and snapshotting easy.
- `keeper/` owns the input log, the state commitment, and the EndBlocker that ties them together.
- `web/` serves the browser client and proxies its transactions to the node.

The IWAD itself is node-local config, not state. Params commit to its sha256, so every node has
to be playing the same game, but nobody ships 4MB of game data in a genesis file.

## Running it

```bash
make doom-start
```

That wipes `~/.gaia-doom`, fetches the Freedoom IWAD, builds a single validator genesis with
`wad_hash` set, and starts the node. Then in another shell:

```bash
make doom-web
```

Click the screen and play. Arrows move and turn, `A`/`D` strafe, `Ctrl` fires, `Space` opens
doors, `Esc` is the menu, `1`-`7` pick weapons.

and open http://127.0.0.1:8666.

The page is three things at once: the screen, a feed of the transactions you're signing, and a
feed of the state commitments coming back. Click any transaction hash and the node looks it up by
hash and hands back the decoded `MsgInput`, block height, gas and fee, which is the part that
makes it obvious none of this is a local emulator.

### Speed

DOOM wants 35 tics a second. Gaia commits a block about every 66ms on a laptop, so the start
script sets `tics_per_block = 2`, which lands around 30. If your hardware makes faster blocks,
drop it to 1 and shorten `timeout_commit`.

The doom EndBlocker itself is not the bottleneck. A tic costs about 1.5ms and hashing the engine's
7MB of memory a few more; an empty gaia block costs about the same with or without it. The chain
does need the transaction indexer on (`indexer = "kv"`) for the clickable hashes, which is worth
about 9ms a block.

## Rebuilding the wasm

Only needed if you touch the C.

```bash
make -C x/doom/wasmbuild toolchain   # one time, fetches wasi-sdk into ~/.wasi-sdk
make doom-wasm
```

The result lands at `engine/doom.wasm` and is committed, so a normal `make build` doesn't need a
C toolchain at all. It has to stay committed, too: the state commitment is a hash of the engine's
memory, so every node has to run the byte-identical module. Two nodes on differently built wasm
would fork.

## Things worth knowing

**Input is edge-triggered and per block.** The pending slot is cleared every block, so holding a
key means sending a transaction every block. A block with no transaction is a released-keys tic,
same as a dropped packet in a vanilla netgame. The browser client just sends continuously.

**Last transaction in a block wins.** This is single player; there's no seat management. Set
`params.controller` if you want to pin the game to one address.

**The input log is never pruned by default.** That's what makes a restarted node able to rebuild
the game. Set `input_history` if you'd rather have the disk back, but then a fresh replay can't
reach tic 0 anymore.

**Changing the WAD mid-game is rejected.** The stored inputs only reproduce the sim for the WAD
they were recorded against.

**The module is inert unless genesis says otherwise.** `wad_hash` defaults to empty, so a normal
gaia chain carries the module and never runs it.

**The browser gets a private key.** `/api/session` hands the page the player key straight out of
the node's keyring. That's fine for a throwaway local chain and nothing else. Signing 35 times a
second through a wallet popup was never going to happen.

## Licensing

`wasmbuild/doomgeneric.tar.gz` is GPL-2.0-or-later (see `wasmbuild/LICENSE`), inherited from
id Software via Chocolate Doom and doomgeneric. `doomgeneric_chain.c` and the compiled
`engine/doom.wasm` are the same license. The rest of gaia is Apache-2.0.

The Go code doesn't link the C, it loads it as a sandboxed wasm module, but the wasm is
`go:embed`ed into the binary so the two ship together. Since the DOOM side is "or later", a
gaiad built with this module is distributed under GPL-3.0. Build without x/doom and gaia stays
Apache-2.0. Full breakdown in the repo's [NOTICE](../../NOTICE).

No game data lives here. `make doom-start` fetches [Freedoom](https://freedoom.github.io)
Phase 1, which is BSD-3-Clause, and the shareware DOOM1.WAD is deliberately not the default
because it isn't ours to hand out. Set `WAD_URL`/`WAD_SHA256` to point at an IWAD you own.

DOOM is a trademark of id Software / ZeniMax. This is not affiliated with or endorsed by them.
