package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	"github.com/cosmos/gaia/v29/x/doom/web"
)

// Flags on `gaiad start` are namespaced so they cannot collide with the
// server's own.
const (
	flagStartWeb      = "doom.web"
	flagStartListen   = "doom.listen"
	flagStartPlayer   = "doom.player"
	flagStartPoll     = "doom.poll"
	flagStartDenom    = "doom.fee-denom"
	flagStartKeyring  = "doom.keyring-backend"
	flagStartSource   = "doom.frames"
	flagStartFrameKey = "doom.frame-key"
)

// AddStartFlags registers the flags that let `gaiad start` serve the browser
// client itself, so playing does not need a second process.
func AddStartFlags(cmd *cobra.Command) {
	cmd.Flags().Bool(flagStartWeb, false, "serve the DOOM browser client alongside the node")
	cmd.Flags().String(flagStartListen, defaultListen, "host:port to serve the browser client on")
	cmd.Flags().String(flagStartPlayer, defaultPlayer, "keyring name of the key the browser plays with")
	cmd.Flags().Duration(flagStartPoll, defaultPoll, "how often to ask the node for a new frame")
	cmd.Flags().String(flagStartDenom, defaultDenom, "denom the browser pays its fee in")
	cmd.Flags().String(flagStartKeyring, keyring.BackendTest, "keyring backend the player key is read from")
	cmd.Flags().String(flagStartSource, web.FrameSourceQuery,
		"where frames come from: 'query' reads them off the node, 'block' pushes them through block data")
	cmd.Flags().String(flagStartFrameKey, defaultFrameKey, "keyring name the node signs MsgFrame with, for --doom.frames=block")
}

// StartWeb runs the browser client in the node's process. It is a PostSetup
// hook, so it is called once the node is up and dies with it.
func StartWeb(svrCtx *server.Context, clientCtx client.Context, ctx context.Context, g *errgroup.Group) error {
	if !svrCtx.Viper.GetBool(flagStartWeb) {
		return nil
	}

	// The in-process CometBFT client is only wired up when the API or gRPC
	// server is on, and every query the browser makes goes through it.
	if clientCtx.Client == nil {
		return fmt.Errorf("--%s needs the api or grpc server enabled in app.toml", flagStartWeb)
	}

	if clientCtx.ChainID == "" {
		genesis, err := genutiltypes.AppGenesisFromFile(svrCtx.Config.GenesisFile())
		if err != nil {
			return fmt.Errorf("read genesis: %w", err)
		}
		clientCtx = clientCtx.WithChainID(genesis.ChainID)
	}

	// `start` has no keyring flags of its own, so build one against the node's
	// home directory.
	kr, err := keyring.New(
		sdk.KeyringServiceName(),
		svrCtx.Viper.GetString(flagStartKeyring),
		clientCtx.HomeDir,
		clientCtx.Input,
		clientCtx.Codec,
	)
	if err != nil {
		return fmt.Errorf("open keyring: %w", err)
	}

	cfg := web.Config{
		ClientCtx:    clientCtx.WithKeyring(kr),
		Listen:       svrCtx.Viper.GetString(flagStartListen),
		PlayerKey:    svrCtx.Viper.GetString(flagStartPlayer),
		PollInterval: svrCtx.Viper.GetDuration(flagStartPoll),
		FeeDenom:     svrCtx.Viper.GetString(flagStartDenom),
		FrameSource:  svrCtx.Viper.GetString(flagStartSource),
		FrameKey:     svrCtx.Viper.GetString(flagStartFrameKey),
	}

	g.Go(func() error {
		return web.Serve(ctx, cfg)
	})

	return nil
}
