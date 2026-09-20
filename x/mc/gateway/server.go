// Package gateway speaks Minecraft to a real client on behalf of the chain.
//
// It is the socket and nothing else. Every button the player pushes leaves here
// as a signed transaction and comes back as a block, and every block it draws
// is built out of what the chain agreed on. There is no world in this package:
// what it holds is a connection and a cursor into the chain's log.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tnze/go-mc/chat"
	"github.com/Tnze/go-mc/data/packetid"
	mcnet "github.com/Tnze/go-mc/net"
	pk "github.com/Tnze/go-mc/net/packet"
	"github.com/Tnze/go-mc/offline"

	"github.com/cosmos/cosmos-sdk/client"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

const (
	// The client this speaks to. go-mc carries the 1.20.2 packet tables, and
	// the handshake is checked against this so a mismatched client is told so
	// rather than left to desync.
	protocolVersion = 764
	protocolName    = "1.20.2"

	// dimensionName is the world the player is in. Its type is described by the
	// registry sent during configuration.
	dimensionName = "minecraft:overworld"

	// playerEntityID is the client's own entity. One seat, so it can be fixed.
	playerEntityID = 1

	// compressionThreshold is where packets start being deflated. A chunk is
	// well over this and a position report is well under.
	compressionThreshold = 256
)

// Config is the input to Serve.
type Config struct {
	ClientCtx client.Context
	// Listen is a host:port for the Minecraft client. 25565 is the port the
	// client fills in for you.
	Listen string
	// PlayerKey names a key in the node's keyring. Everything the player does
	// is signed with it, so it is expected to be a throwaway the chain's setup
	// script funded.
	PlayerKey string
	// FeeDenom is the denom the player pays fees in.
	FeeDenom string
	// ViewDistance is how many chunks out to send, in each direction.
	ViewDistance int
	// Flush is how often a block's worth of actions is packed into a
	// transaction. It wants to be about the block time.
	Flush time.Duration
	// Poll is how often the chain's log is read back.
	Poll time.Duration
}

// Serve runs the Minecraft server until the context is cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Listen == "" {
		cfg.Listen = ":25565"
	}
	if cfg.ViewDistance <= 0 {
		cfg.ViewDistance = 6
	}
	if cfg.Flush <= 0 {
		cfg.Flush = 50 * time.Millisecond
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 15 * time.Millisecond
	}

	ch, err := newChain(cfg.ClientCtx, cfg.PlayerKey, cfg.FeeDenom)
	if err != nil {
		return err
	}

	listener, err := mcnet.ListenMC(cfg.Listen)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	fmt.Printf("mc: %s server on %s, playing as %s\n", protocolName, cfg.Listen, ch.addr)

	// One seat, so one connection. A second client gets told why rather than
	// quietly fighting the first one for the same account sequence.
	var busy atomic.Bool

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		go func(conn mcnet.Conn) {
			defer conn.Close()

			s := &session{cfg: cfg, chain: ch, conn: &conn, urgent: make(chan struct{}, 1)}
			if err := s.serve(ctx, &busy); err != nil {
				fmt.Printf("mc: %v disconnected: %v\n", conn.Socket.RemoteAddr(), err)
			}
		}(conn)
	}
}

// session is one connected client.
type session struct {
	cfg   Config
	chain *chain
	conn  *mcnet.Conn

	params types.Params
	name   string

	// writeMu serialises the socket. The reader, the keepalive and the log
	// poller all write to it.
	writeMu sync.Mutex

	// mu guards what has happened since the last transaction.
	mu      sync.Mutex
	pending []types.Action
	// last is what the client last told us, so a packet that only carries
	// rotation still produces a complete position for the chain.
	last lastKnown

	// slot is the hotbar slot the client last selected, which is what a place
	// puts down.
	slot atomic.Int32

	// slots is what each hotbar slot holds, as a block state. It starts as the
	// palette the gateway handed over and follows the client from there, so
	// anything from the creative inventory can be placed. It is node-local: the
	// chain is told the state that was placed, not what was in the hotbar.
	slots [9]atomic.Uint32

	// urgent wakes the flusher for something the player is watching for.
	urgent chan struct{}

	// predictions holds the sequence number of every edit the client has drawn
	// but the chain has not answered for yet, oldest first. See resolve.
	predictions []int32
}

func (s *session) write(p pk.Packet) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WritePacket(p)
}

func (s *session) serve(ctx context.Context, busy *atomic.Bool) error {
	protocol, intention, err := s.handshake()
	if err != nil {
		return err
	}

	switch intention {
	case 1:
		return s.status(ctx)
	case 2:
		if !busy.CompareAndSwap(false, true) {
			return s.rejectLogin("this world has one seat and somebody is in it")
		}
		defer busy.Store(false)

		if protocol != protocolVersion {
			return s.rejectLogin(fmt.Sprintf("this server is %s, your client is protocol %d", protocolName, protocol))
		}

		return s.play(ctx)
	default:
		return fmt.Errorf("unknown intention %d", intention)
	}
}

func (s *session) handshake() (protocol int32, intention int32, err error) {
	var p pk.Packet
	if err := s.conn.ReadPacket(&p); err != nil {
		return 0, 0, err
	}

	var (
		proto pk.VarInt
		addr  pk.String
		port  pk.UnsignedShort
		next  pk.VarInt
	)
	if err := p.Scan(&proto, &addr, &port, &next); err != nil {
		return 0, 0, err
	}

	return int32(proto), int32(next), nil
}

// status answers the server list. It is the only thing a client can ask for
// without holding the seat.
func (s *session) status(ctx context.Context) error {
	for {
		var p pk.Packet
		if err := s.conn.ReadPacket(&p); err != nil {
			return err
		}

		switch packetid.ServerboundPacketID(p.ID) {
		case packetid.ServerboundStatusRequest:
			tick := uint64(0)
			if st, err := s.chain.state(ctx); err == nil {
				tick = st.State.Tick
			}

			body, err := json.Marshal(map[string]any{
				"version": map[string]any{"name": protocolName, "protocol": protocolVersion},
				"players": map[string]any{"max": 1, "online": 0},
				"description": map[string]any{
					"text": fmt.Sprintf("a cosmos-sdk chain, tick %d", tick),
				},
			})
			if err != nil {
				return err
			}

			if err := s.write(pk.Marshal(packetid.ClientboundStatusResponse, pk.String(body))); err != nil {
				return err
			}

		case packetid.ServerboundStatusPingRequest:
			var payload pk.Long
			if err := p.Scan(&payload); err != nil {
				return err
			}
			return s.write(pk.Marshal(packetid.ClientboundStatusPongResponse, payload))

		default:
			return nil
		}
	}
}

func (s *session) rejectLogin(reason string) error {
	msg, err := json.Marshal(chat.Text(reason))
	if err != nil {
		return err
	}
	return s.write(pk.Marshal(packetid.ClientboundLoginDisconnect, pk.String(msg)))
}

// play takes the client from login to a world it can walk around in.
func (s *session) play(ctx context.Context) error {
	params, err := s.chain.params(ctx)
	if err != nil {
		return fmt.Errorf("read params: %w", err)
	}
	s.params = params

	if err := s.login(); err != nil {
		return err
	}
	if err := s.configure(); err != nil {
		return err
	}

	state, err := s.chain.state(ctx)
	if err != nil {
		return err
	}

	if err := s.join(ctx, state); err != nil {
		return err
	}

	fmt.Printf("mc: %s joined at tick %d\n", s.name, state.State.Tick)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
	}()

	go s.keepalive(ctx)
	go s.flush(ctx)
	go s.follow(ctx, state.State.Tick)

	return s.read(ctx)
}

func (s *session) login() error {
	var p pk.Packet
	if err := s.conn.ReadPacket(&p); err != nil {
		return err
	}

	var (
		name pk.String
		id   pk.UUID
	)
	if err := p.Scan(&name, &id); err != nil {
		return err
	}
	s.name = string(name)

	if err := s.write(pk.Marshal(packetid.ClientboundLoginCompression, pk.VarInt(compressionThreshold))); err != nil {
		return err
	}
	s.conn.SetThreshold(compressionThreshold)

	// Offline mode. There is no account to check against Mojang and nothing
	// worth encrypting on a socket to your own machine.
	uid := offline.NameToUUID(s.name)
	if err := s.write(pk.Marshal(packetid.ClientboundLoginSuccess,
		pk.UUID(uid), name, pk.VarInt(0),
	)); err != nil {
		return err
	}

	for {
		if err := s.conn.ReadPacket(&p); err != nil {
			return err
		}
		if packetid.ServerboundPacketID(p.ID) == packetid.ServerboundLoginAcknowledged {
			return nil
		}
	}
}

// configure hands over the registries the client needs before it will take a
// world. Everything here describes Minecraft rather than the chain.
func (s *session) configure() error {
	if err := s.write(pk.Marshal(packetid.ClientboundConfigRegistryData, pk.NBT(buildCodec()))); err != nil {
		return err
	}
	if err := s.write(pk.Marshal(packetid.ClientboundConfigFinishConfiguration)); err != nil {
		return err
	}

	for {
		var p pk.Packet
		if err := s.conn.ReadPacket(&p); err != nil {
			return err
		}
		if packetid.ServerboundPacketID(p.ID) == packetid.ServerboundConfigFinishConfiguration {
			return nil
		}
	}
}

// join sends the world. Chunks go first so the client has ground under it by
// the time it is told where it is standing.
func (s *session) join(ctx context.Context, state *types.QueryStateResponse) error {
	vd := s.cfg.ViewDistance

	dimensions := []pk.Identifier{dimensionName}

	if err := s.write(pk.Marshal(packetid.ClientboundLogin,
		pk.Int(playerEntityID),
		pk.Boolean(false), // hardcore
		pk.Array(dimensions),
		pk.VarInt(1), // max players
		pk.VarInt(vd),
		pk.VarInt(vd),
		pk.Boolean(false), // reduced debug info
		pk.Boolean(true),  // respawn screen
		pk.Boolean(false), // limited crafting
		pk.Identifier(dimensionTypeName),
		pk.Identifier(dimensionName),
		pk.Long(0),         // hashed seed, only used for biome noise
		pk.UnsignedByte(1), // creative
		pk.Byte(-1),        // no previous gamemode
		pk.Boolean(false),  // not a debug world
		pk.Boolean(true),   // flat, so the horizon is drawn at the right height
		pk.Boolean(false),  // no death location
		pk.VarInt(0),       // portal cooldown
	)); err != nil {
		return err
	}

	// Invulnerable, may fly, creative. Flying is the only way to get around a
	// world with no physics in it.
	if err := s.write(pk.Marshal(packetid.ClientboundPlayerAbilities,
		pk.Byte(0x01|0x04|0x08), pk.Float(0.05), pk.Float(0.1),
	)); err != nil {
		return err
	}

	slot := int32(state.Player.Hotbar) % 9
	s.slot.Store(slot)

	for i, entry := range hotbar {
		s.slots[i].Store(stateID(entry.block))
	}

	if err := s.write(pk.Marshal(packetid.ClientboundSetCarriedItem, pk.Byte(slot))); err != nil {
		return err
	}
	if err := s.sendInventory(); err != nil {
		return err
	}

	spawn := pk.Position{
		X: int(state.Player.X / types.FixedPointScale),
		Y: int(s.params.GroundLevel) + 1,
		Z: int(state.Player.Z / types.FixedPointScale),
	}
	if err := s.write(pk.Marshal(packetid.ClientboundSetDefaultSpawnPosition, spawn, pk.Float(0))); err != nil {
		return err
	}

	cx, cz := types.ChunkOf(int32(spawn.X), int32(spawn.Z))

	if err := s.write(pk.Marshal(packetid.ClientboundSetChunkCacheCenter, pk.VarInt(cx), pk.VarInt(cz))); err != nil {
		return err
	}

	if err := s.sendChunks(ctx, cx, cz); err != nil {
		return err
	}

	return s.write(pk.Marshal(packetid.ClientboundPlayerPosition,
		pk.Double(float64(state.Player.X)/types.FixedPointScale),
		pk.Double(float64(state.Player.Y)/types.FixedPointScale),
		pk.Double(float64(state.Player.Z)/types.FixedPointScale),
		pk.Float(float32(state.Player.Yaw)/types.AngleScale),
		pk.Float(float32(state.Player.Pitch)/types.AngleScale),
		pk.Byte(0),
		pk.VarInt(1), // teleport id, acknowledged by the client
	))
}

// sendInventory fills the hotbar with the fixed palette. The client decides
// nothing here: the gateway knows what each slot places.
func (s *session) sendInventory() error {
	const slots = 46

	content := make([]pk.Field, 0, slots)
	for i := 0; i < slots; i++ {
		if i >= hotbarStart && i < hotbarStart+len(hotbar) {
			item := hotbar[i-hotbarStart].item
			content = append(content, pk.Tuple{
				pk.Boolean(true), pk.VarInt(item), pk.Byte(64), pk.NBT(nil),
			})
			continue
		}
		content = append(content, pk.Tuple{pk.Boolean(false)})
	}

	return s.write(pk.Marshal(packetid.ClientboundContainerSetContent,
		pk.UnsignedByte(0), // the player's own inventory
		pk.VarInt(1),       // state id
		pk.Array(content),
		pk.Tuple{pk.Boolean(false)}, // nothing on the cursor
	))
}

// sendChunks builds the view distance out of the generated world and whatever
// the chain has on top of it.
func (s *session) sendChunks(ctx context.Context, centerX, centerZ int32) error {
	vd := int32(s.cfg.ViewDistance)

	if err := s.write(pk.Marshal(packetid.ClientboundChunkBatchStart)); err != nil {
		return err
	}

	count := 0
	for dx := -vd; dx <= vd; dx++ {
		for dz := -vd; dz <= vd; dz++ {
			cx, cz := centerX+dx, centerZ+dz

			edits, err := s.chain.chunk(ctx, cx, cz)
			if err != nil {
				return fmt.Errorf("chunk %d,%d: %w", cx, cz, err)
			}

			c := buildChunk(s.params.GroundLevel, cx, cz, edits)

			if err := s.write(pk.Marshal(packetid.ClientboundLevelChunkWithLight,
				pk.Int(cx), pk.Int(cz), c,
			)); err != nil {
				return err
			}
			count++
		}
	}

	return s.write(pk.Marshal(packetid.ClientboundChunkBatchFinished, pk.VarInt(count)))
}

// countEdits is how many of a batch the client is holding a prediction for.
func countEdits(batch []types.Action) int {
	n := 0
	for _, action := range batch {
		switch action.Kind.(type) {
		case *types.Action_Dig, *types.Action_Place:
			n++
		}
	}
	return n
}

func (s *session) queue(action types.Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, action)
}

// queueEdit queues a block change and remembers the prediction the client drew
// for it.
//
// An edit does not wait for the next flush. Breaking a block is the one thing
// the player watches for, so it gets its own transaction rather than riding
// along with the next position report. A position report waiting half a flush
// is nothing; a block doing it is the difference between the world feeling live
// and feeling like a query.
func (s *session) queueEdit(sequence int32, action types.Action) {
	s.mu.Lock()
	s.pending = append(s.pending, action)
	s.predictions = append(s.predictions, sequence)
	s.mu.Unlock()

	select {
	case s.urgent <- struct{}{}:
	default:
	}
}

// resolve tells the client it can stop predicting the oldest n edits.
//
// The order matters and is the whole reason ghost blocks happen. An ack means
// "show what I sent you", not "your guess was right", so it has to go out after
// the block update it is answering for. Ack first and the client redraws the
// block the way it was, then flips again when the update turns up.
//
// An edit whose transaction never made it is acked with no update in front of
// it, which is how the block comes back.
func (s *session) resolve(n int) error {
	s.mu.Lock()
	if n > len(s.predictions) {
		n = len(s.predictions)
	}
	if n == 0 {
		s.mu.Unlock()
		return nil
	}
	sequence := s.predictions[n-1]
	s.predictions = s.predictions[n:]
	s.mu.Unlock()

	return s.write(pk.Marshal(packetid.ClientboundBlockChangedAck, pk.VarInt(sequence)))
}

// flush packs everything the player did since the last block into one
// transaction. Twenty position reports a second is a handful per block, which
// is small enough to sign and broadcast at the block rate.
//
// An edit does not wait for the tick, it wakes this instead, so the only thing
// between breaking a block and the chain agreeing is a block.
func (s *session) flush(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Flush)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.urgent:
		}

		s.mu.Lock()
		batch := s.pending
		s.pending = nil
		s.mu.Unlock()

		if len(batch) == 0 {
			continue
		}

		if err := s.chain.send(ctx, batch); err != nil && ctx.Err() == nil {
			fmt.Printf("mc: tick dropped: %v\n", err)

			// Nothing is coming back for these, so resolve them here. The
			// client reverts to the block it last had from the chain, which is
			// the edit not having happened.
			if err := s.resolve(countEdits(batch)); err != nil {
				return
			}
		}
	}
}

// follow turns the chain's log back into packets.
//
// The cursor is the next tick worth reading, not the last one read. A block
// logs against the tick it started on, so a cursor that chased the chain's
// current tick would step over whatever happened in the block it was in.
//
// This is the half that makes the world real: the client already drew its own
// prediction, and what arrives here is the block that agreed with it.
func (s *session) follow(ctx context.Context, cursor uint64) {
	ticker := time.NewTicker(s.cfg.Poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		res, err := s.chain.log(ctx, cursor)
		if err != nil {
			continue
		}

		for _, entry := range res.Entries {
			cursor = entry.Tick + 1

			edits := 0
			for _, action := range entry.Actions {
				drawn, err := s.apply(action)
				if err != nil {
					return
				}
				if drawn {
					edits++
				}
			}

			if err := s.resolve(edits); err != nil {
				return
			}
		}
	}
}

// apply draws one thing the chain agreed happened, and says whether that was a
// block change, which is what the acks are counted against.
func (s *session) apply(action types.Action) (bool, error) {
	switch kind := action.Kind.(type) {
	case *types.Action_Dig:
		return true, s.blockUpdate(kind.Dig.Pos, types.StateAir)

	case *types.Action_Place:
		return true, s.blockUpdate(kind.Place.Pos, kind.Place.State)

	case *types.Action_Chat:
		msg, err := json.Marshal(chat.Text(fmt.Sprintf("<%s> %s", s.name, kind.Chat.Text)))
		if err != nil {
			return false, err
		}
		return false, s.write(pk.Marshal(packetid.ClientboundSystemChat, pk.String(msg), pk.Boolean(false)))

	default:
		// Movement comes back too, but the client is the one that told us where
		// it was. Echoing it would only fight its own prediction.
		return false, nil
	}
}

func (s *session) blockUpdate(pos types.BlockPos, state uint32) error {
	return s.write(pk.Marshal(packetid.ClientboundBlockUpdate,
		pk.Position{X: int(pos.X), Y: int(pos.Y), Z: int(pos.Z)},
		pk.VarInt(state),
	))
}

func (s *session) keepalive(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.write(pk.Marshal(packetid.ClientboundKeepAlive, pk.Long(now.UnixMilli()))); err != nil {
				return
			}
		}
	}
}

// read is the client's half of the conversation.
func (s *session) read(ctx context.Context) error {
	for {
		var p pk.Packet
		if err := s.conn.ReadPacket(&p); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		if err := s.handle(p); err != nil {
			return err
		}
	}
}

func (s *session) handle(p pk.Packet) error {
	switch packetid.ServerboundPacketID(p.ID) {
	case packetid.ServerboundMovePlayerPos:
		var x, y, z pk.Double
		var onGround pk.Boolean
		if err := p.Scan(&x, &y, &z, &onGround); err != nil {
			return err
		}
		s.move(&x, &y, &z, nil, nil, onGround)

	case packetid.ServerboundMovePlayerPosRot:
		var x, y, z pk.Double
		var yaw, pitch pk.Float
		var onGround pk.Boolean
		if err := p.Scan(&x, &y, &z, &yaw, &pitch, &onGround); err != nil {
			return err
		}
		s.move(&x, &y, &z, &yaw, &pitch, onGround)

	case packetid.ServerboundMovePlayerRot:
		var yaw, pitch pk.Float
		var onGround pk.Boolean
		if err := p.Scan(&yaw, &pitch, &onGround); err != nil {
			return err
		}
		s.move(nil, nil, nil, &yaw, &pitch, onGround)

	case packetid.ServerboundMovePlayerStatusOnly:
		var onGround pk.Boolean
		if err := p.Scan(&onGround); err != nil {
			return err
		}
		s.move(nil, nil, nil, nil, nil, onGround)

	case packetid.ServerboundPlayerAction:
		return s.dig(p)

	case packetid.ServerboundUseItemOn:
		return s.place(p)

	case packetid.ServerboundSetCarriedItem:
		var slot pk.Short
		if err := p.Scan(&slot); err != nil {
			return err
		}
		s.slot.Store(int32(slot) % 9)
		s.queue(types.Action{Kind: &types.Action_Hotbar{Hotbar: &types.Hotbar{Slot: uint32(slot)}}})

	case packetid.ServerboundSetCreativeModeSlot:
		return s.creativeSlot(p)

	case packetid.ServerboundChat:
		var text pk.String
		if err := p.Scan(&text); err != nil {
			return err
		}
		s.queue(types.Action{Kind: &types.Action_Chat{Chat: &types.Chat{Text: string(text)}}})
	}

	return nil
}

// lastKnown is the client's most recent full position.
type lastKnown struct {
	x, y, z    int64
	yaw, pitch int32
}

func (s *session) move(x, y, z *pk.Double, yaw, pitch *pk.Float, onGround pk.Boolean) {
	s.mu.Lock()
	if x != nil {
		s.last.x = int64(float64(*x) * types.FixedPointScale)
		s.last.y = int64(float64(*y) * types.FixedPointScale)
		s.last.z = int64(float64(*z) * types.FixedPointScale)
	}
	if yaw != nil {
		s.last.yaw = int32(float32(*yaw) * types.AngleScale)
		s.last.pitch = int32(float32(*pitch) * types.AngleScale)
	}
	last := s.last
	s.pending = append(s.pending, types.Action{Kind: &types.Action_Move{Move: &types.Move{
		X: last.x, Y: last.y, Z: last.z,
		Yaw: last.yaw, Pitch: last.pitch,
		OnGround: bool(onGround),
	}}})
	s.mu.Unlock()
}

// dig breaks a block.
//
// The client has already drawn the hole and keeps drawing it until it is acked,
// which happens once the chain has said the same thing. See resolve.
func (s *session) dig(p pk.Packet) error {
	var (
		status   pk.VarInt
		pos      pk.Position
		face     pk.Byte
		sequence pk.VarInt
	)
	if err := p.Scan(&status, &pos, &face, &sequence); err != nil {
		return err
	}

	// Creative breaks a block the moment the button goes down, so only the
	// start of the dig is a real edit. Anything else is acked on the spot
	// because there is nothing coming back for it.
	if status != 0 {
		return s.write(pk.Marshal(packetid.ClientboundBlockChangedAck, sequence))
	}

	s.queueEdit(int32(sequence), types.Action{Kind: &types.Action_Dig{Dig: &types.Dig{
		Pos: types.BlockPos{X: int32(pos.X), Y: int32(pos.Y), Z: int32(pos.Z)},
	}}})

	return nil
}

// place puts down whatever the selected hotbar slot holds.
func (s *session) place(p pk.Packet) error {
	var (
		hand             pk.VarInt
		pos              pk.Position
		face             pk.VarInt
		curX, curY, curZ pk.Float
		insideBlock      pk.Boolean
		sequence         pk.VarInt
	)
	if err := p.Scan(&hand, &pos, &face, &curX, &curY, &curZ, &insideBlock, &sequence); err != nil {
		return err
	}

	state := s.slots[s.slot.Load()].Load()
	if state == types.StateAir {
		// Holding something that is not a block. Nothing is placed, so the
		// client's guess is resolved on the spot rather than left hanging.
		return s.write(pk.Marshal(packetid.ClientboundBlockChangedAck, sequence))
	}

	target := offsetByFace(pos, int(face))

	s.queueEdit(int32(sequence), types.Action{Kind: &types.Action_Place{Place: &types.Place{
		Pos:   types.BlockPos{X: int32(target.X), Y: int32(target.Y), Z: int32(target.Z)},
		State: state,
	}}})

	return nil
}

// hotbarStart is where the hotbar sits in the player's inventory.
const hotbarStart = 36

// creativeSlot follows what the client puts in its hotbar.
//
// In creative the client is the authority on its own inventory and just tells
// the server what it did, which is how the whole block list becomes available
// without the gateway knowing anything about recipes or creative tabs.
func (s *session) creativeSlot(p pk.Packet) error {
	var (
		slot    pk.Short
		present pk.Boolean
	)
	if err := p.Scan(&slot, &present); err != nil {
		return err
	}

	index := int(slot) - hotbarStart
	if index < 0 || index >= len(s.slots) {
		return nil
	}

	if !present {
		s.slots[index].Store(types.StateAir)
		return nil
	}

	var itemID pk.VarInt
	if err := p.Scan(&slot, &present, &itemID); err != nil {
		return err
	}

	state, ok := stateForItem(uint32(itemID))
	if !ok {
		state = types.StateAir
	}
	s.slots[index].Store(state)

	return nil
}

// offsetByFace steps one block off the face that was clicked, which is where
// the new block goes.
func offsetByFace(pos pk.Position, face int) pk.Position {
	switch face {
	case 0:
		pos.Y--
	case 1:
		pos.Y++
	case 2:
		pos.Z--
	case 3:
		pos.Z++
	case 4:
		pos.X--
	case 5:
		pos.X++
	}
	return pos
}
