package gateway

import (
	"context"
	"fmt"
	"sync"

	"github.com/cosmos/cosmos-sdk/client"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/mc/types"
)

// tickGas covers a transaction carrying a block's worth of play. A position
// report is a handful of bytes and a block holds about twenty of them, so this
// is mostly the fixed cost of a transaction.
const tickGas = 400_000

// tickFee is well over the minimum the local chain asks for. Playing spends a
// transaction per block, so the account behind it is expected to be a throwaway
// the chain's setup script funded.
const tickFee = 1000

// chain is the gateway's side of the node: it reads the world out and puts the
// player's actions back in.
type chain struct {
	clientCtx client.Context
	query     types.QueryClient
	keyName   string
	feeDenom  string
	addr      sdk.AccAddress

	mu     sync.Mutex
	accNum uint64
	seq    uint64
}

func newChain(clientCtx client.Context, keyName, feeDenom string) (*chain, error) {
	record, err := clientCtx.Keyring.Key(keyName)
	if err != nil {
		return nil, fmt.Errorf("player key %q: %w", keyName, err)
	}

	addr, err := record.GetAddress()
	if err != nil {
		return nil, err
	}

	num, seq, err := clientCtx.AccountRetriever.GetAccountNumberSequence(clientCtx, addr)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", addr, err)
	}

	return &chain{
		clientCtx: clientCtx,
		query:     types.NewQueryClient(clientCtx),
		keyName:   keyName,
		feeDenom:  feeDenom,
		addr:      addr,
		accNum:    num,
		seq:       seq,
	}, nil
}

func (c *chain) params(ctx context.Context) (types.Params, error) {
	res, err := c.query.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		return types.Params{}, err
	}
	return res.Params, nil
}

func (c *chain) state(ctx context.Context) (*types.QueryStateResponse, error) {
	return c.query.State(ctx, &types.QueryStateRequest{})
}

func (c *chain) log(ctx context.Context, fromTick uint64) (*types.QueryLogResponse, error) {
	return c.query.Log(ctx, &types.QueryLogRequest{FromTick: fromTick})
}

func (c *chain) chunk(ctx context.Context, cx, cz int32) ([]types.BlockEdit, error) {
	res, err := c.query.Chunk(ctx, &types.QueryChunkRequest{X: cx, Z: cz})
	if err != nil {
		return nil, err
	}
	return res.Edits, nil
}

// send signs one block's worth of actions and drops it in the mempool.
//
// Nothing waits for it. The client has already drawn whatever it did locally,
// and the chain's answer comes back through the log a block later, which is the
// only thing that makes it real.
func (c *chain) send(ctx context.Context, actions []types.Action) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	msg := &types.MsgTick{Player: c.addr.String(), Actions: actions}

	txf := clienttx.Factory{}.
		WithChainID(c.clientCtx.ChainID).
		WithKeybase(c.clientCtx.Keyring).
		WithTxConfig(c.clientCtx.TxConfig).
		WithAccountRetriever(c.clientCtx.AccountRetriever).
		WithAccountNumber(c.accNum).
		WithSequence(c.seq).
		WithGas(tickGas).
		WithFees(sdk.NewCoins(sdk.NewInt64Coin(c.feeDenom, tickFee)).String())

	txb, err := txf.BuildUnsignedTx(msg)
	if err != nil {
		return err
	}

	if err := clienttx.Sign(ctx, txf, c.keyName, txb, true); err != nil {
		return err
	}

	raw, err := c.clientCtx.TxConfig.TxEncoder()(txb.GetTx())
	if err != nil {
		return err
	}

	res, err := c.clientCtx.BroadcastTxSync(raw)
	if err != nil {
		c.resync()
		return err
	}
	if res.Code != 0 {
		c.resync()
		return fmt.Errorf("tick rejected: %s", res.RawLog)
	}

	c.seq++
	return nil
}

// resync puts the sequence back where the chain thinks it is. A rejected
// transaction leaves the local counter one ahead, and every following one would
// be rejected too.
func (c *chain) resync() {
	if _, seq, err := c.clientCtx.AccountRetriever.GetAccountNumberSequence(c.clientCtx, c.addr); err == nil {
		c.seq = seq
	}
}
