package web

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tnze/go-mc/level/block"

	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/cosmos/cosmos-sdk/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// history is how many blocks the page can scroll back through. The walker
// keeps them so a browser that connects late has something to show, and so a
// transaction hash stays clickable for a while after it scrolls off.
const history = 400

// ActionView is one thing the player did, as the explorer shows it.
type ActionView struct {
	Kind  string      `json:"kind"`
	Pos   *[3]int32   `json:"pos,omitempty"`
	Block string      `json:"block,omitempty"`
	Text  string      `json:"text,omitempty"`
	Slot  *uint32     `json:"slot,omitempty"`
	At    *[3]float64 `json:"at,omitempty"`
}

// Summary is the action counts, so a row can be read without expanding it.
type Summary struct {
	Moves  int `json:"moves"`
	Breaks int `json:"breaks"`
	Places int `json:"places"`
	Chats  int `json:"chats"`
	Hotbar int `json:"hotbar"`
}

// TxView is one transaction, decoded out of block data.
type TxView struct {
	Hash     string `json:"hash"`
	Height   int64  `json:"height"`
	Time     int64  `json:"time"`
	Index    int    `json:"index"`
	Signer   string `json:"signer"`
	Sequence uint64 `json:"sequence"`
	Code     uint32 `json:"code"`
	// Known is false when the node was not keeping its ABCI responses, so
	// whether the transaction was accepted cannot be said. See
	// discard_abci_responses.
	Known     bool         `json:"known"`
	Log       string       `json:"log,omitempty"`
	GasWanted int64        `json:"gasWanted"`
	GasUsed   int64        `json:"gasUsed"`
	Fee       string       `json:"fee"`
	Size      int          `json:"size"`
	Summary   Summary      `json:"summary"`
	Actions   []ActionView `json:"actions"`
}

// BlockView is one block.
type BlockView struct {
	Height  int64    `json:"height"`
	Time    int64    `json:"time"`
	Hash    string   `json:"hash"`
	AppHash string   `json:"appHash"`
	Txs     []TxView `json:"txs"`
}

// StateView is what the chain currently thinks the world is.
type StateView struct {
	Tick      uint64     `json:"tick"`
	StateHash string     `json:"stateHash"`
	Player    [3]float64 `json:"player"`
	Address   string     `json:"address"`
}

// walker follows the chain a block at a time and decodes what it finds.
//
// Everything the page shows comes from block data and block results, so it
// works with the transaction indexer off. That is not a workaround: a
// transaction that is in a block is in a block, and reading it back out of one
// is a stronger claim than asking an index about it.
type walker struct {
	clientCtx client.Context
	query     types.QueryClient

	mu     sync.RWMutex
	blocks []BlockView
	byHash map[string]*TxView
}

func newWalker(clientCtx client.Context) *walker {
	return &walker{
		clientCtx: clientCtx,
		query:     types.NewQueryClient(clientCtx),
		byHash:    make(map[string]*TxView),
	}
}

// recent returns the newest blocks first.
func (w *walker) recent(n int) []BlockView {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if n > len(w.blocks) {
		n = len(w.blocks)
	}

	out := make([]BlockView, 0, n)
	for i := len(w.blocks) - 1; i >= len(w.blocks)-n; i-- {
		out = append(out, w.blocks[i])
	}
	return out
}

func (w *walker) tx(hash string) (TxView, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	tx, ok := w.byHash[strings.ToUpper(hash)]
	if !ok {
		return TxView{}, false
	}
	return *tx, true
}

func (w *walker) keep(b BlockView) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.blocks = append(w.blocks, b)
	for i := range w.blocks[len(w.blocks)-1].Txs {
		tx := &w.blocks[len(w.blocks)-1].Txs[i]
		w.byHash[tx.Hash] = tx
	}

	if len(w.blocks) > history {
		dropped := w.blocks[0]
		w.blocks = w.blocks[1:]
		for _, tx := range dropped.Txs {
			delete(w.byHash, tx.Hash)
		}
	}
}

// run walks forward from the chain's head until the context is cancelled.
func (w *walker) run(ctx context.Context, emit func(BlockView)) error {
	status, err := w.clientCtx.Client.Status(ctx)
	if err != nil {
		return fmt.Errorf("node status: %w", err)
	}
	height := status.SyncInfo.LatestBlockHeight

	// Consecutive misses on the height being fetched. Asking the node whether
	// it has the block costs a round trip, so a few quick retries come first.
	misses := 0

	for {
		if ctx.Err() != nil {
			return nil
		}

		view, err := w.at(ctx, height)
		if err != nil {
			misses++

			// A height above the head has not happened yet, so wait a fraction
			// of a block for it. Anything else is a block that exists and would
			// not decode, and retrying that forever would peg the explorer at
			// one height while the chain ran away from it.
			//
			// The retries are not politeness. The fetch and the check are two
			// calls, the chain commits between them often enough at this block
			// rate, and skipping on the first miss drops real blocks.
			if misses < 8 || w.pending(ctx, height) {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(10 * time.Millisecond):
				}
				continue
			}

			fmt.Printf("mc: explorer skipped block %d: %v\n", height, err)
			height++
			misses = 0
			continue
		}

		height++
		misses = 0

		w.keep(view)
		emit(view)
	}
}

// pending reports whether a height simply has not been committed yet.
func (w *walker) pending(ctx context.Context, height int64) bool {
	status, err := w.clientCtx.Client.Status(ctx)
	if err != nil {
		// No answer from the node is worth waiting on rather than skipping past
		// blocks it may well have.
		return true
	}
	return height > status.SyncInfo.LatestBlockHeight
}

// at decodes one block.
func (w *walker) at(ctx context.Context, height int64) (BlockView, error) {
	blk, err := w.clientCtx.Client.Block(ctx, &height)
	if err != nil {
		return BlockView{}, err
	}

	view := BlockView{
		Height:  blk.Block.Height,
		Time:    blk.Block.Time.UnixMilli(),
		Hash:    strings.ToUpper(hex.EncodeToString(blk.BlockID.Hash)),
		AppHash: strings.ToUpper(hex.EncodeToString(blk.Block.AppHash)),
		// Empty rather than absent, so the page can count it without checking.
		Txs: []TxView{},
	}

	if len(blk.Block.Txs) == 0 {
		return view, nil
	}

	// Results are a separate call and are what say whether a transaction was
	// accepted, since a block carries rejected transactions too.
	results := w.results(ctx, height)

	decoder := w.clientCtx.TxConfig.TxDecoder()

	for i, raw := range blk.Block.Txs {
		decoded, err := decoder(raw)
		if err != nil {
			continue
		}

		tx := TxView{
			Hash:   strings.ToUpper(hex.EncodeToString(cmttypes.Tx(raw).Hash())),
			Height: view.Height,
			Time:   view.Time,
			Index:  i,
			Size:   len(raw),
		}

		if fee, ok := decoded.(sdk.FeeTx); ok {
			tx.GasWanted = int64(fee.GetGas())
			tx.Fee = fee.GetFee().String()
		}

		if sigs, ok := decoded.(authsigning.SigVerifiableTx); ok {
			if list, err := sigs.GetSignaturesV2(); err == nil && len(list) > 0 {
				tx.Sequence = list[0].Sequence
			}
		}

		if results != nil && i < len(results.TxsResults) {
			res := results.TxsResults[i]
			tx.Known = true
			tx.Code = res.Code
			tx.GasUsed = res.GasUsed
			if res.Code != 0 {
				tx.Log = res.Log
			}
		}

		mine := false
		for _, msg := range decoded.GetMsgs() {
			tick, ok := msg.(*types.MsgTick)
			if !ok {
				continue
			}
			mine = true
			tx.Signer = tick.Player
			for _, action := range tick.Actions {
				tx.Actions = append(tx.Actions, describe(action, &tx.Summary))
			}
		}

		// Somebody else's transaction landing in the same block is not part of
		// this world, so the explorer leaves it alone.
		if !mine {
			continue
		}

		view.Txs = append(view.Txs, tx)
	}

	return view, nil
}

// results fetches a block's transaction results, or nil.
//
// The walker runs at the head, and the state store can trail the block store by
// a moment there, so the first miss usually means early rather than missing. It
// stays nil for a node running with discard_abci_responses on, which threw them
// away and is never going to answer.
func (w *walker) results(ctx context.Context, height int64) *coretypes.ResultBlockResults {
	for attempt := 0; attempt < 3; attempt++ {
		res, err := w.clientCtx.Client.BlockResults(ctx, &height)
		if err == nil {
			return res
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Millisecond):
		}
	}
	return nil
}

// state asks the chain where the world is now.
func (w *walker) state(ctx context.Context) (StateView, error) {
	res, err := w.query.State(ctx, &types.QueryStateRequest{})
	if err != nil {
		return StateView{}, err
	}

	return StateView{
		Tick:      res.State.Tick,
		StateHash: strings.ToUpper(hex.EncodeToString(res.State.StateHash)),
		Address:   res.Player.Address,
		Player: [3]float64{
			float64(res.Player.X) / types.FixedPointScale,
			float64(res.Player.Y) / types.FixedPointScale,
			float64(res.Player.Z) / types.FixedPointScale,
		},
	}, nil
}

// describe turns one logged action into something the page can render, and
// counts it.
func describe(action types.Action, sum *Summary) ActionView {
	switch kind := action.Kind.(type) {
	case *types.Action_Dig:
		sum.Breaks++
		p := kind.Dig.Pos
		return ActionView{Kind: "break", Pos: &[3]int32{p.X, p.Y, p.Z}}

	case *types.Action_Place:
		sum.Places++
		p := kind.Place.Pos
		return ActionView{
			Kind:  "place",
			Pos:   &[3]int32{p.X, p.Y, p.Z},
			Block: blockName(kind.Place.State),
		}

	case *types.Action_Chat:
		sum.Chats++
		return ActionView{Kind: "chat", Text: kind.Chat.Text}

	case *types.Action_Hotbar:
		sum.Hotbar++
		slot := kind.Hotbar.Slot
		return ActionView{Kind: "hotbar", Slot: &slot}

	case *types.Action_Move:
		sum.Moves++
		m := kind.Move
		return ActionView{Kind: "move", At: &[3]float64{
			float64(m.X) / types.FixedPointScale,
			float64(m.Y) / types.FixedPointScale,
			float64(m.Z) / types.FixedPointScale,
		}}

	default:
		return ActionView{Kind: "unknown"}
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
