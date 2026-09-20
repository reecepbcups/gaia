// Package cli holds the gaiad subcommands for the mc module.
package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Tnze/go-mc/level/block"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/cosmos/gaia/v29/x/mc/gateway"
	"github.com/cosmos/gaia/v29/x/mc/types"
	"github.com/cosmos/gaia/v29/x/mc/web"
)

const (
	flagMoves  = "moves"
	flagFrom   = "from"
	flagListen = "listen"
	flagPlayer = "player"
	flagDenom  = "fee-denom"
	flagView   = "view-distance"
	flagWeb    = "web"
	flagFlush  = "flush"
	flagPoll   = "poll"
)

// Defaults for `gaiad mc serve`.
const (
	// 25565 is the port the client fills in for you.
	defaultListen = "127.0.0.1:25565"
	defaultPlayer = "player"
	defaultDenom  = "stake"
	defaultView   = 6
	// The explorer. 8666 is x/doom's, so this sits next to it.
	defaultWeb = "127.0.0.1:8667"
	// Roughly the block time, so a block's worth of movement goes in one
	// transaction rather than several. Edits do not wait for it.
	defaultFlush = 50 * time.Millisecond
	// Well under the block time, so a block's edits are drawn in the block they
	// land in rather than the one after.
	defaultPoll = 15 * time.Millisecond
)

// NewRootCmd returns the `gaiad mc` command tree.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Play the Minecraft world running on this chain",
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		newServeCmd(),
		newStateCmd(),
		newChunkCmd(),
		newWatchCmd(),
	)

	return cmd
}

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the Minecraft protocol against the chain's world",
		Long: `Serve the Minecraft protocol against the chain's world.

Speaks 1.20.2 to an unmodified client. Everything the player does leaves as a
signed transaction and the world it draws is built out of the chain's log, so
the blocks you break only disappear once a block says they did.

A block explorer is served alongside it, which walks the chain and decodes the
transactions your play is arriving in.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			listen, err := cmd.Flags().GetString(flagListen)
			if err != nil {
				return err
			}
			player, err := cmd.Flags().GetString(flagPlayer)
			if err != nil {
				return err
			}
			denom, err := cmd.Flags().GetString(flagDenom)
			if err != nil {
				return err
			}
			view, err := cmd.Flags().GetInt(flagView)
			if err != nil {
				return err
			}
			flush, err := cmd.Flags().GetDuration(flagFlush)
			if err != nil {
				return err
			}
			poll, err := cmd.Flags().GetDuration(flagPoll)
			if err != nil {
				return err
			}

			webListen, err := cmd.Flags().GetString(flagWeb)
			if err != nil {
				return err
			}

			g, ctx := errgroup.WithContext(cmd.Context())

			g.Go(func() error {
				return gateway.Serve(ctx, gateway.Config{
					ClientCtx:    clientCtx,
					Listen:       listen,
					PlayerKey:    player,
					FeeDenom:     denom,
					ViewDistance: view,
					Flush:        flush,
					Poll:         poll,
				})
			})

			// The explorer only reads, so losing it is not worth taking the
			// game down for, but a port already in use should be said out loud.
			if webListen != "" {
				g.Go(func() error {
					return web.Serve(ctx, web.Config{ClientCtx: clientCtx, Listen: webListen})
				})
			}

			return g.Wait()
		},
	}

	cmd.Flags().String(flagListen, defaultListen, "host:port to serve the Minecraft protocol on")
	cmd.Flags().String(flagPlayer, defaultPlayer, "keyring name of the key the player signs with")
	cmd.Flags().String(flagDenom, defaultDenom, "denom the player pays fees in")
	cmd.Flags().Int(flagView, defaultView, "how many chunks to send in each direction")
	cmd.Flags().Duration(flagFlush, defaultFlush, "how often a block's worth of actions is broadcast")
	cmd.Flags().Duration(flagPoll, defaultPoll, "how often the chain's log is read back")
	cmd.Flags().String(flagWeb, defaultWeb, "host:port for the block explorer, empty to turn it off")
	flags.AddQueryFlagsToCmd(cmd)
	// The gateway signs with a key out of the node's keyring, so this needs
	// more than the query flags.
	flags.AddKeyringFlags(cmd.Flags())

	return cmd
}

func newStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Show the current tick, its commitment and where the player is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			res, err := types.NewQueryClient(clientCtx).State(cmd.Context(), &types.QueryStateRequest{})
			if err != nil {
				return err
			}

			return clientCtx.PrintProto(res)
		},
	}

	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func newChunkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chunk [x] [z]",
		Short: "Show the blocks in one chunk that differ from the generated world",
		Long: `Show the blocks in one chunk that differ from the generated world.

Chunk coordinates, not block ones: divide by 16. This is the whole of what the
chain stores about the world, so an empty answer means nobody has built there.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			x, err := strconv.ParseInt(args[0], 10, 32)
			if err != nil {
				return err
			}
			z, err := strconv.ParseInt(args[1], 10, 32)
			if err != nil {
				return err
			}

			res, err := types.NewQueryClient(clientCtx).Chunk(cmd.Context(),
				&types.QueryChunkRequest{X: int32(x), Z: int32(z)})
			if err != nil {
				return err
			}

			return clientCtx.PrintProto(res)
		},
	}

	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Follow the world's log as it commits",
		Long: `Follow the world's log as it commits.

This is the same thing the gateway reads to build its packets, so what shows up
here is exactly what the chain agreed happened, in the order it agreed. Position
reports are left out by default because there are twenty of them a second.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			poll, err := cmd.Flags().GetDuration(flagPoll)
			if err != nil {
				return err
			}
			moves, err := cmd.Flags().GetBool(flagMoves)
			if err != nil {
				return err
			}
			from, err := cmd.Flags().GetUint64(flagFrom)
			if err != nil {
				return err
			}

			query := types.NewQueryClient(clientCtx)

			// Zero means start from whatever the world is on now, so the
			// command opens on live play rather than replaying the whole log.
			cursor := from
			if cursor == 0 {
				state, err := query.State(cmd.Context(), &types.QueryStateRequest{})
				if err != nil {
					return err
				}
				cursor = state.State.Tick
			}

			ticker := time.NewTicker(poll)
			defer ticker.Stop()

			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-ticker.C:
				}

				res, err := query.Log(cmd.Context(), &types.QueryLogRequest{FromTick: cursor})
				if err != nil {
					return err
				}

				for _, entry := range res.Entries {
					cursor = entry.Tick + 1
					for _, action := range entry.Actions {
						if line := describe(action, moves); line != "" {
							fmt.Printf("tick %-8d %s\n", entry.Tick, line)
						}
					}
				}
			}
		},
	}

	cmd.Flags().Duration(flagPoll, defaultPoll, "how often to ask the node for new entries")
	cmd.Flags().Bool(flagMoves, false, "include position reports")
	cmd.Flags().Uint64(flagFrom, 0, "tick to start from, 0 for the current one")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// describe turns one logged action into a line, or an empty string for one not
// worth printing.
func describe(action types.Action, moves bool) string {
	switch kind := action.Kind.(type) {
	case *types.Action_Dig:
		p := kind.Dig.Pos
		return fmt.Sprintf("break  %d,%d,%d", p.X, p.Y, p.Z)

	case *types.Action_Place:
		p := kind.Place.Pos
		return fmt.Sprintf("place  %s at %d,%d,%d", blockName(kind.Place.State), p.X, p.Y, p.Z)

	case *types.Action_Chat:
		return fmt.Sprintf("chat   %s", kind.Chat.Text)

	case *types.Action_Hotbar:
		return fmt.Sprintf("hotbar slot %d", kind.Hotbar.Slot)

	case *types.Action_Move:
		if !moves {
			return ""
		}
		m := kind.Move
		return fmt.Sprintf("move   %.2f,%.2f,%.2f",
			float64(m.X)/types.FixedPointScale,
			float64(m.Y)/types.FixedPointScale,
			float64(m.Z)/types.FixedPointScale)

	default:
		return ""
	}
}

// blockName resolves a state id against the client's palette. The chain only
// ever sees the number.
func blockName(state uint32) string {
	if int(state) >= len(block.StateList) {
		return fmt.Sprintf("state %d", state)
	}
	return block.StateList[state].ID()
}
