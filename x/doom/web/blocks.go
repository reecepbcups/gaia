package web

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"time"

	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

// Where the browser's frames come from.
//
// The default reads them off the node, which is the sane thing: the framebuffer
// is derived from the sim, so a client can rebuild it and the chain never has
// to carry it. FrameSourceBlock carries it anyway, as a MsgFrame per block, and
// the browser is fed out of block data alone. It is slower, far bigger and buys
// nothing. It is also the only arrangement where the bytes on screen were
// literally in a block, which is the point of having it.
const (
	FrameSourceQuery = "query"
	FrameSourceBlock = "block"
)

const (
	// frameGas covers a transaction whose body is a compressed screen. Most of
	// it is the per-byte size cost.
	frameGas = 3_000_000

	// frameFee is well over the minimum the local chain asks for. The pusher's
	// account is funded by the start script and nothing else spends from it.
	frameFee = 1000
)

// pushFrames signs one MsgFrame per block and broadcasts it, so that the
// screen ends up in block data.
//
// The frame it pushes is the last tic of a block, which is the only one the
// engine still holds when the transaction executes a block later. Anything
// earlier has been drawn over by then and could not be checked, so it is not
// worth putting on chain.
func (s *server) pushFrames(ctx context.Context) error {
	record, err := s.cfg.ClientCtx.Keyring.Key(s.cfg.FrameKey)
	if err != nil {
		return fmt.Errorf("frame key %q: %w", s.cfg.FrameKey, err)
	}

	addr, err := record.GetAddress()
	if err != nil {
		return err
	}

	num, seq, err := s.cfg.ClientCtx.AccountRetriever.GetAccountNumberSequence(s.cfg.ClientCtx, addr)
	if err != nil {
		return fmt.Errorf("frame account %s: %w", addr, err)
	}

	fmt.Printf("doom: pushing frames into block data as %s\n", addr)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	var cursor uint64

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		res, err := s.query.Frames(ctx, &types.QueryFramesRequest{AfterTic: cursor})
		if err != nil || len(res.Frames) == 0 {
			continue
		}

		frame := res.Frames[len(res.Frames)-1]
		cursor = frame.Tic

		msg := &types.MsgFrame{
			Submitter: addr.String(),
			Tic:       frame.Tic,
			Pixels:    types.RunLengthEncode(frame.Pixels),
			Palette:   frame.Palette,
		}

		if err := s.broadcastFrame(ctx, msg, num, &seq); err != nil {
			// A frame that misses its block is rejected on purpose, and the
			// sequence has to be put back where the chain thinks it is.
			if _, resync, rerr := s.cfg.ClientCtx.AccountRetriever.GetAccountNumberSequence(s.cfg.ClientCtx, addr); rerr == nil {
				seq = resync
			}
		}
	}
}

// broadcastFrame signs msg with the node's own key and sends it to the mempool.
func (s *server) broadcastFrame(ctx context.Context, msg *types.MsgFrame, num uint64, seq *uint64) error {
	clientCtx := s.cfg.ClientCtx

	txf := clienttx.Factory{}.
		WithChainID(clientCtx.ChainID).
		WithKeybase(clientCtx.Keyring).
		WithTxConfig(clientCtx.TxConfig).
		WithAccountRetriever(clientCtx.AccountRetriever).
		WithAccountNumber(num).
		WithSequence(*seq).
		WithGas(frameGas).
		WithFees(sdk.NewCoins(sdk.NewInt64Coin(s.cfg.FeeDenom, frameFee)).String())

	txb, err := txf.BuildUnsignedTx(msg)
	if err != nil {
		return err
	}

	if err := clienttx.Sign(ctx, txf, s.cfg.FrameKey, txb, true); err != nil {
		return err
	}

	raw, err := clientCtx.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return err
	}

	res, err := clientCtx.BroadcastTxSync(raw)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("frame tx rejected: %s", res.RawLog)
	}

	*seq++
	return nil
}

// streamFromBlocks feeds the browser out of block data and nothing else.
//
// It walks the chain a block at a time, decodes the transactions, and pulls the
// screen out of whatever MsgFrame it finds. No query, no engine: if the bytes
// were not in a block, nothing is drawn. One frame per block is all there is,
// so this runs at the block rate rather than at DOOM's 35Hz, and pacing it
// would only add delay to a frame rate that is already the bottleneck.
func (s *server) streamFromBlocks(w http.ResponseWriter, r *http.Request, flusher http.Flusher) {
	clientCtx := s.cfg.ClientCtx
	decoder := clientCtx.TxConfig.TxDecoder()

	status, err := clientCtx.Client.Status(r.Context())
	if err != nil {
		return
	}
	height := status.SyncInfo.LatestBlockHeight

	buf := make([]byte, 4+frameHeaderSize+engine.FrameSize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(frameHeaderSize+engine.FrameSize))

	var lastTic uint64

	for {
		if r.Context().Err() != nil {
			return
		}

		block, err := clientCtx.Client.Block(r.Context(), &height)
		if err != nil {
			// Not produced yet. The block rate is the frame rate here, so
			// waiting a fraction of one is the right amount.
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		height++

		for _, raw := range block.Block.Txs {
			decoded, err := decoder(raw)
			if err != nil {
				continue
			}

			for _, msg := range decoded.GetMsgs() {
				frame, ok := msg.(*types.MsgFrame)
				if !ok || frame.Tic <= lastTic {
					// A rejected frame is still in the block. It is stale by
					// definition, which the tic gives away without having to
					// go and fetch the block's results.
					continue
				}

				pixels, err := types.RunLengthDecode(frame.Pixels, engine.FrameSize)
				if err != nil {
					continue
				}
				lastTic = frame.Tic

				prefix := buf[4 : 4+framePrefixSize]
				for i := range prefix {
					prefix[i] = 0
				}
				binary.LittleEndian.PutUint64(prefix[0:8], frame.Tic)
				// The app hash of the block the frame travelled in. In this
				// mode that is the commitment the picture arrived under.
				copy(prefix[8:8+stateHashSize], block.Block.AppHash)
				binary.LittleEndian.PutUint64(prefix[40:48], uint64(block.Block.Height))
				binary.LittleEndian.PutUint64(prefix[48:56], uint64(block.Block.Time.UnixMilli()))

				copy(buf[4+framePrefixSize:4+frameHeaderSize], frame.Palette)
				copy(buf[4+frameHeaderSize:], pixels)

				if _, err := w.Write(buf); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
