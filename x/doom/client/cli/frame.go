package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

const flagPNG = "png"

// newFrameCmd reads a screen back out of block data.
//
// This only finds anything on a chain whose node is running with
// `--frames block`. The default arrangement never puts a picture in a block,
// so there is nothing here to read: use `doom state` and a replay instead.
func newFrameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "frame [height]",
		Short: "Pull the frame out of a block",
		Long: `Decode the MsgFrame carried by a block and report the screen it holds.

Only useful against a chain pushing frames through block data. Without a height
it walks back from the tip until it finds one.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			height, exact := int64(0), len(args) == 1
			if exact {
				if height, err = strconv.ParseInt(args[0], 10, 64); err != nil {
					return fmt.Errorf("height %q: %w", args[0], err)
				}
			}

			out, err := cmd.Flags().GetString(flagPNG)
			if err != nil {
				return err
			}

			return runFrame(cmd, clientCtx, height, exact, out)
		},
	}

	cmd.Flags().String(flagPNG, "", "write the screen here as a png")
	flags.AddQueryFlagsToCmd(cmd)

	return cmd
}

// searchDepth is how far back an unspecified height looks. A chain pushing
// frames carries one in nearly every block, so anything deeper means the
// pusher is not running.
const searchDepth = 20

func runFrame(cmd *cobra.Command, clientCtx client.Context, height int64, exact bool, out string) error {
	ctx := cmd.Context()

	if height == 0 {
		status, err := clientCtx.Client.Status(ctx)
		if err != nil {
			return err
		}
		height = status.SyncInfo.LatestBlockHeight
	}

	decoder := clientCtx.TxConfig.TxDecoder()

	depth := searchDepth
	if exact {
		// An explicit height means that block and no other.
		depth = 1
	}

	for d := 0; d < depth; d++ {
		at := height - int64(d)
		if at < 1 {
			break
		}

		block, err := clientCtx.Client.Block(ctx, &at)
		if err != nil {
			return err
		}

		for _, raw := range block.Block.Txs {
			decoded, err := decoder(raw)
			if err != nil {
				continue
			}

			for _, msg := range decoded.GetMsgs() {
				frame, ok := msg.(*types.MsgFrame)
				if !ok {
					continue
				}

				pixels, err := types.RunLengthDecode(frame.Pixels, engine.FrameSize)
				if err != nil {
					return fmt.Errorf("block %d carries a frame that does not decode: %w", at, err)
				}

				cmd.Printf("height     %d\n", at)
				cmd.Printf("tic        %d\n", frame.Tic)
				cmd.Printf("submitter  %s\n", frame.Submitter)
				cmd.Printf("on chain   %d bytes, %.2fx off %d raw\n",
					len(frame.Pixels), float64(engine.FrameSize)/float64(len(frame.Pixels)), engine.FrameSize)
				cmd.Printf("app hash   %X\n", block.Block.AppHash)

				if out == "" {
					return nil
				}

				f, err := os.Create(out)
				if err != nil {
					return err
				}
				defer f.Close()

				if err := engine.EncodePNG(f, pixels, frame.Palette); err != nil {
					return err
				}

				cmd.Printf("png        %s\n", out)
				return nil
			}
		}
	}

	if exact {
		return fmt.Errorf("no frame in block %d", height)
	}
	return fmt.Errorf("no frame in the %d blocks below %d; is the node running with --frames block?", searchDepth, height)
}
