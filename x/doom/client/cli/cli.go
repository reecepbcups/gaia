// Package cli holds the gaiad subcommands for the doom module.
package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"

	"github.com/cosmos/gaia/v29/x/doom/types"
	"github.com/cosmos/gaia/v29/x/doom/web"
)

const (
	flagListen   = "listen"
	flagPlayer   = "player"
	flagPoll     = "poll"
	flagDenom    = "fee-denom"
	flagSource   = "frames"
	flagFrameKey = "frame-key"
)

// Defaults shared by `gaiad doom web` and the same server run from `gaiad start`.
const (
	defaultListen = "127.0.0.1:8666"
	defaultPlayer = "player"
	// Polling is cheap and only ever returns tics the client has not seen yet,
	// so it runs well ahead of the block rate to keep frames fresh.
	defaultPoll  = 10 * time.Millisecond
	defaultDenom = "stake"
	// defaultFrameKey is only used when frames are pushed through block data.
	defaultFrameKey = "frames"
)

// NewRootCmd returns the `gaiad doom` command tree.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Play the DOOM running on this chain",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		newWebCmd(),
		newStateCmd(),
		newFrameCmd(),
		newInputCmd(),
	)

	return cmd
}

func newWebCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the browser client",
		Long: `Serve the browser client.

Streams the chain's framebuffer to a canvas and hands the browser a throwaway
key so it signs and broadcasts its own input. Every keypress is a transaction.`,
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
			poll, err := cmd.Flags().GetDuration(flagPoll)
			if err != nil {
				return err
			}
			denom, err := cmd.Flags().GetString(flagDenom)
			if err != nil {
				return err
			}
			source, err := cmd.Flags().GetString(flagSource)
			if err != nil {
				return err
			}
			frameKey, err := cmd.Flags().GetString(flagFrameKey)
			if err != nil {
				return err
			}

			return web.Serve(cmd.Context(), web.Config{
				ClientCtx:    clientCtx,
				Listen:       listen,
				PlayerKey:    player,
				PollInterval: poll,
				FeeDenom:     denom,
				FrameSource:  source,
				FrameKey:     frameKey,
			})
		},
	}

	cmd.Flags().String(flagListen, defaultListen, "host:port to serve the browser client on")
	cmd.Flags().String(flagPlayer, defaultPlayer, "keyring name of the key the browser plays with")
	cmd.Flags().Duration(flagPoll, defaultPoll, "how often to ask the node for a new frame")
	cmd.Flags().String(flagDenom, defaultDenom, "denom the browser pays its fee in")
	cmd.Flags().String(flagSource, web.FrameSourceQuery,
		"where frames come from: 'query' reads them off the node, 'block' pushes them through block data")
	cmd.Flags().String(flagFrameKey, defaultFrameKey, "keyring name the node signs MsgFrame with, for --frames=block")
	flags.AddQueryFlagsToCmd(cmd)
	// The browser plays with a key out of the node's keyring, so this command
	// needs more than the query flags.
	flags.AddKeyringFlags(cmd.Flags())

	return cmd
}

func newStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Show the current tic and state commitment",
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

func newInputCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "input [buttons]",
		Short: "Hold down a button mask for one block",
		Long: fmt.Sprintf(`Hold down a button mask for one block.

buttons is a decimal or 0x-prefixed bit mask. Bit meanings are in
proto/gaia/doom/v1/doom.proto, for example fire is bit %d and use is bit %d.`,
			types.BUTTON_FIRE, types.BUTTON_USE),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			var buttons uint32
			if _, err := fmt.Sscanf(args[0], "%v", &buttons); err != nil {
				return fmt.Errorf("parse buttons %q: %w", args[0], err)
			}

			msg := &types.MsgInput{
				Player:  clientCtx.GetFromAddress().String(),
				Buttons: buttons,
			}

			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}
