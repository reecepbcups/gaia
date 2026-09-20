// Command probe is a headless Minecraft client, enough of one to check the
// gateway end to end without a launcher.
//
// It logs in, walks through configuration, counts what it is sent, then moves,
// breaks a block and puts one down. Whether any of that worked is a question
// for the chain, not for this: check `gaiad mc state` afterwards.
//
//	go run ./x/mc/scripts/probe -addr 127.0.0.1:25565
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Tnze/go-mc/data/item"
	"github.com/Tnze/go-mc/data/packetid"
	"github.com/Tnze/go-mc/level"
	"github.com/Tnze/go-mc/level/block"
	mcnet "github.com/Tnze/go-mc/net"
	pk "github.com/Tnze/go-mc/net/packet"
	"github.com/google/uuid"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:25565", "gateway address")
	name := flag.String("name", "probe", "player name")
	watch := flag.Duration("watch", 3*time.Second, "how long to read packets for")
	flag.Parse()

	conn, err := mcnet.DialMC(*addr)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Handshake, then straight into login.
	must(conn.WritePacket(pk.Marshal(0x00,
		pk.VarInt(764), pk.String("127.0.0.1"), pk.UnsignedShort(25565), pk.VarInt(2),
	)), "handshake")

	must(conn.WritePacket(pk.Marshal(packetid.ServerboundLoginStart,
		pk.String(*name), pk.UUID(uuid.New()),
	)), "login start")

	var p pk.Packet
	for {
		must(conn.ReadPacket(&p), "read login")

		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundLoginCompression:
			var threshold pk.VarInt
			must(p.Scan(&threshold), "compression")
			conn.SetThreshold(int(threshold))
			fmt.Println("compression threshold", threshold)

		case packetid.ClientboundLoginDisconnect:
			var reason pk.String
			_ = p.Scan(&reason)
			log.Fatalf("disconnected: %s", reason)

		case packetid.ClientboundLoginSuccess:
			fmt.Println("login ok")
			must(conn.WritePacket(pk.Marshal(packetid.ServerboundLoginAcknowledged)), "login ack")
			goto configure
		}
	}

configure:
	must(conn.WritePacket(pk.Marshal(packetid.ServerboundConfigClientInformation,
		pk.String("en_us"), pk.Byte(8), pk.VarInt(0), pk.Boolean(true),
		pk.UnsignedByte(0x7f), pk.VarInt(1), pk.Boolean(true), pk.Boolean(true),
	)), "client information")

	for {
		must(conn.ReadPacket(&p), "read config")

		switch packetid.ClientboundPacketID(p.ID) {
		case packetid.ClientboundConfigRegistryData:
			fmt.Println("registry data", len(p.Data), "bytes")

		case packetid.ClientboundConfigDisconnect:
			var reason pk.String
			_ = p.Scan(&reason)
			log.Fatalf("disconnected in configuration: %s", reason)

		case packetid.ClientboundConfigFinishConfiguration:
			must(conn.WritePacket(pk.Marshal(packetid.ServerboundConfigFinishConfiguration)), "finish config")
			goto play
		}
	}

play:
	// One reader for the whole session. Bounding it with a read deadline instead
	// would leave the stream desynced halfway through a packet.
	var (
		mu      sync.Mutex
		counts  = map[string]int{}
		spawned = make(chan [3]float64, 1)
		updates []string
	)

	go func() {
		for {
			var p pk.Packet
			if err := conn.ReadPacket(&p); err != nil {
				return
			}

			id := packetid.ClientboundPacketID(p.ID)

			mu.Lock()
			counts[id.String()]++
			first := counts[id.String()] == 1
			mu.Unlock()

			switch id {
			case packetid.ClientboundLevelChunkWithLight:
				// Decoding one proves the chunk the gateway built is a chunk,
				// which is most of what a real client does with it.
				if first {
					describeChunk(p)
				}

			case packetid.ClientboundPlayerPosition:
				var px, py, pz pk.Double
				var yaw, pitch pk.Float
				var flags pk.Byte
				var teleport pk.VarInt
				must(p.Scan(&px, &py, &pz, &yaw, &pitch, &flags, &teleport), "position")
				must(conn.WritePacket(pk.Marshal(packetid.ServerboundAcceptTeleportation, teleport)), "accept teleport")
				select {
				case spawned <- [3]float64{float64(px), float64(py), float64(pz)}:
				default:
				}

			case packetid.ClientboundBlockUpdate:
				var pos pk.Position
				var state pk.VarInt
				must(p.Scan(&pos, &state), "block update")
				mu.Lock()
				updates = append(updates, fmt.Sprintf("update  %v is now %s", pos, blockName(int(state))))
				mu.Unlock()

			case packetid.ClientboundBlockChangedAck:
				// Order is the thing being checked here. An ack ahead of the
				// update it answers for is what a ghost block looks like on the
				// wire.
				var sequence pk.VarInt
				must(p.Scan(&sequence), "block changed ack")
				mu.Lock()
				updates = append(updates, fmt.Sprintf("ack     sequence %d", sequence))
				mu.Unlock()

			case packetid.ClientboundDisconnect:
				var reason pk.String
				_ = p.Scan(&reason)
				log.Fatalf("disconnected in play: %s", reason)
			}
		}
	}()

	var pos [3]float64
	select {
	case pos = <-spawned:
		fmt.Printf("spawned at %.2f %.2f %.2f\n", pos[0], pos[1], pos[2])
	case <-time.After(*watch):
		log.Fatal("never got a position, the world never arrived")
	}

	x, y, z := pos[0], pos[1], pos[2]

	// Walk a block east, a report at a time, the way a client does.
	for i := 1; i <= 20; i++ {
		must(conn.WritePacket(pk.Marshal(packetid.ServerboundMovePlayerPos,
			pk.Double(x+float64(i)*0.05), pk.Double(y), pk.Double(z), pk.Boolean(true),
		)), "move")
		time.Sleep(50 * time.Millisecond)
	}

	// Put something the default hotbar does not hold into the first slot, the
	// way the creative inventory does, and select it. Anything the client can
	// pick has to be placeable.
	must(conn.WritePacket(pk.Marshal(packetid.ServerboundSetCreativeModeSlot,
		pk.Short(36), pk.Boolean(true), pk.VarInt(item.DiamondBlock.ID), pk.Byte(1), pk.NBT(nil),
	)), "creative slot")
	must(conn.WritePacket(pk.Marshal(packetid.ServerboundSetCarriedItem, pk.Short(0))), "carried item")

	// Break the block under the feet, then put one on top of its neighbour.
	under := pk.Position{X: int(x), Y: int(y) - 1, Z: int(z)}
	must(conn.WritePacket(pk.Marshal(packetid.ServerboundPlayerAction,
		pk.VarInt(0), under, pk.Byte(1), pk.VarInt(1),
	)), "dig")

	beside := pk.Position{X: int(x) + 1, Y: int(y) - 1, Z: int(z)}
	must(conn.WritePacket(pk.Marshal(packetid.ServerboundUseItemOn,
		pk.VarInt(0), beside, pk.VarInt(1),
		pk.Float(0.5), pk.Float(1), pk.Float(0.5),
		pk.Boolean(false), pk.VarInt(2),
	)), "place")

	fmt.Printf("dug %v, placed on top of %v\n", under, beside)

	// Give the chain a couple of blocks to agree.
	time.Sleep(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	pretty, _ := json.MarshalIndent(counts, "", "  ")
	fmt.Println("received:", string(pretty))

	if len(updates) == 0 {
		log.Fatal("the chain never sent a block update back")
	}

	fmt.Println("in this order:")
	for _, u := range updates {
		fmt.Println(" ", u)
	}
}

// describeChunk reads a chunk back the way the client would and prints the
// column under the middle of it.
func describeChunk(p pk.Packet) {
	var cx, cz pk.Int
	c := level.EmptyChunk(24)

	if err := p.Scan(&cx, &cz, c); err != nil {
		log.Fatalf("chunk decode: %v", err)
	}

	fmt.Printf("chunk %d,%d decoded, %d sections\n", cx, cz, len(c.Sections))

	for _, y := range []int{-64, 0, 60, 61, 64, 65} {
		rel := y + 64
		sec, idx := rel>>4, (rel&15)*256
		b := block.StateList[c.Sections[sec].GetBlock(idx)]
		fmt.Printf("  y=%-4d %s\n", y, b.ID())
	}
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}

// blockName resolves a state id against the client's palette, the same way the
// gateway does on the way in.
func blockName(state int) string {
	if state < 0 || state >= len(block.StateList) {
		return fmt.Sprintf("state %d", state)
	}
	return block.StateList[state].ID()
}
