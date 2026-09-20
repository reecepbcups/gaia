package gateway

import (
	"github.com/cosmos/gaia/v29/x/mc/types"
)

// The registry codec is the pile of data a 1.20.2 client is sent during the
// configuration phase, before it will accept a world. It is not consensus
// state: it describes what a Minecraft client is, not what the chain agreed on,
// so it lives here next to the socket.
//
// Only what the client actually looks up is here. Vanilla ships hundreds of
// biomes; a superflat world made of one biome needs exactly one.

type registry[E any] struct {
	Type  string     `nbt:"type"`
	Value []entry[E] `nbt:"value"`
}

type entry[E any] struct {
	Name    string `nbt:"name"`
	ID      int32  `nbt:"id"`
	Element E      `nbt:"element"`
}

type networkCodec struct {
	ChatType      registry[chatType]   `nbt:"minecraft:chat_type"`
	DamageType    registry[damageType] `nbt:"minecraft:damage_type"`
	DimensionType registry[dimension]  `nbt:"minecraft:dimension_type"`
	TrimMaterial  registry[struct{}]   `nbt:"minecraft:trim_material"`
	TrimPattern   registry[struct{}]   `nbt:"minecraft:trim_pattern"`
	WorldGenBiome registry[biome]      `nbt:"minecraft:worldgen/biome"`
}

type dimension struct {
	HasSkylight                 bool    `nbt:"has_skylight"`
	HasCeiling                  bool    `nbt:"has_ceiling"`
	Ultrawarm                   bool    `nbt:"ultrawarm"`
	Natural                     bool    `nbt:"natural"`
	CoordinateScale             float64 `nbt:"coordinate_scale"`
	BedWorks                    bool    `nbt:"bed_works"`
	RespawnAnchorWorks          byte    `nbt:"respawn_anchor_works"`
	MinY                        int32   `nbt:"min_y"`
	Height                      int32   `nbt:"height"`
	LogicalHeight               int32   `nbt:"logical_height"`
	InfiniteBurn                string  `nbt:"infiniburn"`
	Effects                     string  `nbt:"effects"`
	AmbientLight                float64 `nbt:"ambient_light"`
	PiglinSafe                  byte    `nbt:"piglin_safe"`
	HasRaids                    byte    `nbt:"has_raids"`
	MonsterSpawnLightLevel      int32   `nbt:"monster_spawn_light_level"`
	MonsterSpawnBlockLightLimit int32   `nbt:"monster_spawn_block_light_limit"`
}

type biomeEffects struct {
	SkyColor      int32 `nbt:"sky_color"`
	WaterFogColor int32 `nbt:"water_fog_color"`
	FogColor      int32 `nbt:"fog_color"`
	WaterColor    int32 `nbt:"water_color"`
}

type biome struct {
	HasPrecipitation bool         `nbt:"has_precipitation"`
	Temperature      float32      `nbt:"temperature"`
	Downfall         float32      `nbt:"downfall"`
	Effects          biomeEffects `nbt:"effects"`
}

type chatDecoration struct {
	TranslationKey string   `nbt:"translation_key"`
	Parameters     []string `nbt:"parameters"`
	Style          struct{} `nbt:"style"`
}

type chatType struct {
	Chat      chatDecoration `nbt:"chat"`
	Narration chatDecoration `nbt:"narration"`
}

type damageType struct {
	MessageID  string  `nbt:"message_id"`
	Scaling    string  `nbt:"scaling"`
	Exhaustion float32 `nbt:"exhaustion"`
}

// The overworld, flat and always daylight. MinY and Height have to agree with
// what the chunk builder sends or the client drops sections on the floor.
const (
	dimensionTypeName = "minecraft:overworld"
	biomeName         = "minecraft:plains"
)

// damageTypeNames is vanilla's list. The client never takes damage in a
// creative flat world, but the registry has to be there for it to sync.
var damageTypeNames = []string{
	"minecraft:in_fire", "minecraft:lightning_bolt", "minecraft:on_fire",
	"minecraft:lava", "minecraft:hot_floor", "minecraft:in_wall",
	"minecraft:cramming", "minecraft:drown", "minecraft:starve",
	"minecraft:cactus", "minecraft:fall", "minecraft:fly_into_wall",
	"minecraft:out_of_world", "minecraft:generic", "minecraft:magic",
	"minecraft:wither", "minecraft:dragon_breath", "minecraft:dry_out",
	"minecraft:sweet_berry_bush", "minecraft:freeze", "minecraft:stalagmite",
	"minecraft:outside_border", "minecraft:generic_kill", "minecraft:falling_block",
	"minecraft:falling_anvil", "minecraft:falling_stalactite", "minecraft:sting",
	"minecraft:mob_attack", "minecraft:mob_attack_no_aggro", "minecraft:player_attack",
	"minecraft:arrow", "minecraft:trident", "minecraft:mob_projectile",
	"minecraft:fireworks", "minecraft:unattributed_fireball", "minecraft:fireball",
	"minecraft:wither_skull", "minecraft:thrown", "minecraft:indirect_magic",
	"minecraft:thorns", "minecraft:explosion", "minecraft:player_explosion",
	"minecraft:sonic_boom", "minecraft:bad_respawn_point",
}

// buildCodec is what gets handed to the client during configuration.
func buildCodec() networkCodec {
	damage := make([]entry[damageType], len(damageTypeNames))
	for i, name := range damageTypeNames {
		damage[i] = entry[damageType]{
			Name:    name,
			ID:      int32(i),
			Element: damageType{MessageID: "generic", Scaling: "when_caused_by_living_non_player"},
		}
	}

	chatDeco := chatDecoration{
		TranslationKey: "chat.type.text",
		Parameters:     []string{"sender", "content"},
	}

	return networkCodec{
		ChatType: registry[chatType]{
			Type: "minecraft:chat_type",
			Value: []entry[chatType]{{
				Name:    "minecraft:chat",
				ID:      0,
				Element: chatType{Chat: chatDeco, Narration: chatDeco},
			}},
		},
		DamageType: registry[damageType]{
			Type:  "minecraft:damage_type",
			Value: damage,
		},
		DimensionType: registry[dimension]{
			Type: "minecraft:dimension_type",
			Value: []entry[dimension]{{
				Name: dimensionTypeName,
				ID:   0,
				Element: dimension{
					HasSkylight:                 true,
					CoordinateScale:             1,
					BedWorks:                    true,
					MinY:                        types.MinY,
					Height:                      types.WorldHeight,
					LogicalHeight:               types.WorldHeight,
					InfiniteBurn:                "#minecraft:infiniburn_overworld",
					Effects:                     "minecraft:overworld",
					AmbientLight:                0,
					MonsterSpawnLightLevel:      0,
					MonsterSpawnBlockLightLimit: 0,
				},
			}},
		},
		TrimMaterial: registry[struct{}]{Type: "minecraft:trim_material"},
		TrimPattern:  registry[struct{}]{Type: "minecraft:trim_pattern"},
		WorldGenBiome: registry[biome]{
			Type: "minecraft:worldgen/biome",
			Value: []entry[biome]{{
				Name: biomeName,
				ID:   0,
				Element: biome{
					Temperature: 0.8,
					Downfall:    0.4,
					Effects: biomeEffects{
						SkyColor:      0x78A7FF,
						WaterFogColor: 0x050533,
						FogColor:      0xC0D8FF,
						WaterColor:    0x3F76E4,
					},
				},
			}},
		},
	}
}
