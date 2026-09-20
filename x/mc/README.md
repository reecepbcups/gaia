# x/mc

Minecraft as a cosmos-sdk module. The world is consensus state and an
unmodified 1.20.2 client connects straight to it.

There's no game server anywhere. What you're standing on is whatever the chain's
last block committed to, and every block you break leaves your machine as a
signed transaction before it disappears.

## Why this isn't a decompiled Paper

The obvious thing to try is to take Paper or Spigot and run it under consensus.
That doesn't work, and it isn't close. The JVM iterates hash maps in whatever
order it likes, `java.util.Random` gets seeded off the clock, worldgen is
floating point, and half the server is threads talking to each other. Two
validators running it would fork on the first tick.

What is portable is the wire protocol. The client doesn't care what's on the
other end of the socket, so the server is written here in Go and the parts that
have to be agreed on are the only parts that go through consensus.

## How it's split

```text
client  --TCP 25565-->  gateway  --signed MsgTick-->  mempool
   ^                       |                             |
   |                       |                         EndBlocker
   |                       |                             |
   +---- chunks, block updates ----  chain's log  <-------+
```

- **Consensus** holds the block edits, where the player is, the hotbar slot,
  chat, and the tick counter. That's it. A superflat world nobody has touched
  costs nothing, because only what differs from the generated world is stored.
- **The gateway** is the socket and nothing else: varint framing, compression,
  keepalives, registries, chunk serialization. It's node-local, the same way
  x/doom's browser client is. It decides nothing.

`keeper/` owns the world and the EndBlocker. `gateway/` owns the protocol.
Nothing in `keeper/` imports a Minecraft library, and nothing in `gateway/`
writes state.

**Blocks are the world clock.** A block runs `ticks_per_block` ticks in its
EndBlocker, so the chain at height N puts the world at tick N times that. If the
chain stalls the world freezes. Nothing in here reads a wall clock.

## Running it

```bash
make mc-start
```

That wipes `~/.gaia-mc`, builds a single validator genesis and starts the node.
Then in another shell:

```bash
make mc-serve
```

Add a server in your 1.20.2 client pointing at `127.0.0.1:25565` and join. You
spawn on a flat grass world at y=65 in creative, with nine blocks in the hotbar.
Fly around, break things, build things.

To check it's really going through the chain, break a block and then ask:

```bash
gaiad mc chunk 0 0 --home ~/.gaia-mc
gaiad mc state --home ~/.gaia-mc
```

The hole you just made is an entry in there. Nothing else about the world is.

## Watching it

`mc serve` also puts a block explorer on <http://127.0.0.1:8667>. It walks the
chain a block at a time, decodes the transactions your play is arriving in, and
streams them to the page: a block feed, an edit feed, and the decoded `MsgTick`
behind any hash you click, with its signer, sequence, gas, fee and size.

Everything on it comes out of the node's `/block` and `/block_results`, so it
never touches the transaction indexer. A hash being there means the transaction
was in a block, which is a stronger claim than an index agreeing it existed.
`--web ""` turns it off.

For the same thing in a terminal:

```bash
gaiad mc watch --home ~/.gaia-mc
```

That tails the log as it commits, which is the same thing the gateway reads to
build its packets, so what shows up is exactly what the chain agreed happened
and in the order it agreed:

```text
tick 7641     place  minecraft:stone at 13,65,10
tick 7674     break  12,65,10
tick 7686     place  minecraft:stone at 13,66,10
```

Position reports are left out by default because there are twenty a second;
`--moves` puts them back. `--from 0` opens on live play, and any other tick
replays the log from there, so `--from 1` prints everything that has ever
happened in the world.

The chain only ever sees a state id, so the block names come from the client's
palette on this end.

## Speed

Minecraft wants 20 ticks a second, and with `ticks_per_block = 1` that means 20
blocks a second. Measured on a laptop: 16.65 with the transaction indexer on and
18.45 with it off, which is what the start script does, because nothing here
looks a transaction up by hash. That leaves it about 8% short of 20.

Don't bother chasing `timeout_commit`. x/doom measured 0ms and 28ms landing in
the same place, so the ~60ms is propose, vote, execute and commit rather than
the wait after it. The script sets it to 0ms anyway.

The client doesn't mind either way. It runs its own render loop and
interpolates, so a slow clock isn't visible; what runs slow is anything the tick
counter drives, which right now is nothing at all. Push `ticks_per_block` to 2
and the world runs at twice the block rate instead, which is wrong in the other
direction.

Movement doesn't depend on the tick rate. The client sends about twenty position
reports a second and every one is batched into the block that was open when it
arrived, so the chain has your whole path at 20Hz whatever its own clock does.

### What you actually feel

Breaking a block draws instantly, because the client predicts it and is left
predicting until the chain answers. The delay is in the chain agreeing, not in
the block disappearing. That round trip is three things:

- **The wait for a transaction.** Movement rides a 50ms flush. An edit doesn't
  wait for it, it wakes the flusher and gets its own transaction, so this is
  about zero.
- **A block.** 50 to 60ms, and the floor.
- **The wait for the log poll.** 15ms.

A block's worth of play is one transaction, about 400k gas.

## What's real and what isn't

The part worth being precise about, since "on chain" gets used loosely.

**Real.** Block edits, player position, hotbar, chat and the tick counter all go
through the mempool and are committed. The state hash chains the previous
commitment with the block's input and the player it produced, so a node that
applied the log differently fails the apphash rather than quietly forking. The
chunks the client is sent are built from the chain's edits every time, so a
restarted gateway draws the same world.

**Not real, on purpose.** The gateway generates the flat part of the world
rather than reading it, because both ends can derive it from `ground_level`.
Lighting is full daylight everywhere instead of a lighting engine. The registry
the client syncs at login describes Minecraft, not the chain.

**Not real, and it's a limitation.** Movement is taken at face value. Minecraft
has always been client authoritative about position, so the chain records a
claim, and there's no physics or reach check to say otherwise.

### Ghost blocks, and the ack

Worth knowing about because it looks like a bug in the chain and isn't.

Since 1.19 the client predicts its own block changes and tags each one with a
sequence number. `BlockChangedAck` does not mean "accepted", it means "stop
predicting and show what I sent you". So the ack has to go out *after* the
block update it answers for. Send it the moment the click arrives, the way this
used to, and the client drops its prediction, redraws the block the way it was,
and flips again a block later when the update turns up. That flicker is the
ghost.

So an edit is held: the client keeps drawing its guess, the chain's update goes
out when the log produces it, and the ack follows. If the transaction never
lands, the ack goes out with no update in front of it and the block comes back,
which is the correct answer rather than a ghost that survives until you reload
the chunk.

## Limits

**One seat.** Whoever sends the first transaction owns the player and everyone
else is turned away at login. There's no entity tracking, so a second player
would be invisible to the first anyway.

**No simulation.** No mobs, no redstone, no water, no gravity, no day cycle.
The world only changes because somebody put a block somewhere. Adding any of it
means writing a deterministic sim in the keeper, which is the interesting part
and is not done.

**The hotbar starts fixed but doesn't stay that way.** The gateway hands over
nine blocks to start with, and from there the client's own creative inventory
works: picking something puts it in a slot, the gateway follows along, and 855
of the 1255 items resolve to a block. What the slots hold is node-local, so a
reconnect puts the starting nine back. What was placed is not, it's in the log.

**A block with properties goes down in one state.** Stairs face one way and logs
lie on one axis, because working out the right state means reading the face that
was clicked and where the player is standing. The block is the right block.

**The gateway signs for you.** It uses a key out of the node's keyring. That's
fine for a throwaway local chain and nothing else.

**The log is never pruned.** That's what makes an exported world replayable from
tick 0. It also means the chain keeps every position report forever.

## Version

1.20.2, protocol 764. The handshake checks it and tells you if your client is
something else.

That version is where [go-mc](https://github.com/Tnze/go-mc) has its packet
tables, chunk palettes and block state ids, which is most of the protocol work.
Moving to 1.21 means writing the packets that changed since.

## Licensing

No Mojang code or assets are here, and none are downloaded. `go-mc` is MIT. You
bring your own client and your own account.

Minecraft is a trademark of Mojang Studios. This isn't affiliated with or
endorsed by them.
