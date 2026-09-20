// Package web serves the browser client that plays the chain's DOOM.
//
// It is a thin local shim, not a game server: it streams frames it reads out of
// the node and proxies signed transactions through to the node's RPC so the
// browser is not fighting CORS. The browser holds the key and signs its own
// input, so every keypress really does go through the mempool.
package web

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gaia/v29/x/doom/engine"
	"github.com/cosmos/gaia/v29/x/doom/types"
)

//go:embed static
var staticFiles embed.FS

// keyringPassphrase is used to round-trip the player key out of the keyring.
// The armor never leaves this process, so the value only has to be consistent.
const keyringPassphrase = "doom"

// Config is the input to Serve.
type Config struct {
	ClientCtx client.Context
	// Listen is a host:port for the browser client.
	Listen string
	// PlayerKey names a key in the node's keyring. The browser is handed its
	// private key so it can sign its own input; the key is expected to be a
	// throwaway funded by the chain's setup script.
	PlayerKey string
	// PollInterval is how often the node is asked for a new frame.
	PollInterval time.Duration
	// FeeDenom is the denom the browser pays its fee in.
	FeeDenom string
}

type server struct {
	cfg   Config
	query types.QueryClient
}

// Serve runs the browser client until the context is cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.PollInterval <= 0 {
		// DOOM's native rate. Polling faster than the chain produces frames just
		// burns queries.
		cfg.PollInterval = time.Second / 35
	}

	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}

	s := &server{cfg: cfg, query: types.NewQueryClient(cfg.ClientCtx)}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(content)))
	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/account", s.handleAccount)
	mux.HandleFunc("/api/frames", s.handleFrames)
	mux.HandleFunc("/api/tx", s.handleTx)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	fmt.Printf("doom: play at http://%s\n", cfg.Listen)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

type sessionResponse struct {
	ChainID       string `json:"chainId"`
	Address       string `json:"address"`
	PrivateKey    string `json:"privateKey"`
	PublicKey     string `json:"publicKey"`
	AccountNumber uint64 `json:"accountNumber"`
	Sequence      uint64 `json:"sequence"`
	FeeDenom      string `json:"feeDenom"`
	TicsPerBlock  uint32 `json:"ticsPerBlock"`
	Width         uint32 `json:"width"`
	Height        uint32 `json:"height"`
}

// handleSession hands the browser everything it needs to sign, including the
// player's private key. That is only safe because this server is meant to be
// reachable from the machine running the node and the key is a throwaway.
func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PlayerKey == "" {
		httpError(w, http.StatusPreconditionFailed, fmt.Errorf("no player key configured, start with --player"))
		return
	}

	armor, err := s.cfg.ClientCtx.Keyring.ExportPrivKeyArmor(s.cfg.PlayerKey, keyringPassphrase)
	if err != nil {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("export %q: %w", s.cfg.PlayerKey, err))
		return
	}

	priv, _, err := crypto.UnarmorDecryptPrivKey(armor, keyringPassphrase)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err)
		return
	}

	addr := sdk.AccAddress(priv.PubKey().Address())

	params, err := s.query.Params(r.Context(), &types.QueryParamsRequest{})
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}

	num, seq, err := s.cfg.ClientCtx.AccountRetriever.GetAccountNumberSequence(s.cfg.ClientCtx, addr)
	if err != nil {
		httpError(w, http.StatusBadGateway, fmt.Errorf("account %s: %w", addr, err))
		return
	}

	writeJSON(w, sessionResponse{
		ChainID:       s.cfg.ClientCtx.ChainID,
		Address:       addr.String(),
		PrivateKey:    hex.EncodeToString(priv.Bytes()),
		PublicKey:     hex.EncodeToString(priv.PubKey().Bytes()),
		AccountNumber: num,
		Sequence:      seq,
		FeeDenom:      s.cfg.FeeDenom,
		TicsPerBlock:  params.Params.TicsPerBlock,
		Width:         engine.ScreenWidth,
		Height:        engine.ScreenHeight,
	})
}

func (s *server) handleAccount(w http.ResponseWriter, r *http.Request) {
	addr, err := sdk.AccAddressFromBech32(r.URL.Query().Get("address"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	num, seq, err := s.cfg.ClientCtx.AccountRetriever.GetAccountNumberSequence(s.cfg.ClientCtx, addr)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, map[string]uint64{"accountNumber": num, "sequence": seq})
}

// Each streamed frame is a fixed prefix (the tic it was rendered at and the
// state commitment for that tic) followed by the palette and the pixels.
const (
	stateHashSize   = 32
	frameHeaderSize = 8 + stateHashSize + engine.PaletteSize
)

// handleFrames streams the screen as length-prefixed binary chunks. Binary over
// chunked HTTP keeps this dependency free; a 64KB paletted frame 35 times a
// second is small enough that a websocket would not buy anything.
func (s *server) handleFrames(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	buf := make([]byte, 4+frameHeaderSize+engine.FrameSize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(frameHeaderSize+engine.FrameSize))

	var lastTic uint64
	var seen bool

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}

		frame, err := s.query.Frame(r.Context(), &types.QueryFrameRequest{})
		if err != nil {
			// The node may not have booted the game yet. Keep polling.
			continue
		}

		if seen && frame.Tic == lastTic {
			continue
		}
		lastTic, seen = frame.Tic, true

		binary.LittleEndian.PutUint64(buf[4:12], frame.Tic)
		hash := buf[12 : 12+stateHashSize]
		for i := range hash {
			hash[i] = 0
		}
		copy(hash, frame.StateHash)
		copy(buf[12+stateHashSize:12+stateHashSize+engine.PaletteSize], frame.Palette)
		copy(buf[12+stateHashSize+engine.PaletteSize:], frame.Pixels)

		if _, err := w.Write(buf); err != nil {
			return
		}
		flusher.Flush()
	}
}

type txRequest struct {
	// Tx is a base64 encoded TxRaw.
	Tx string `json:"tx"`
}

// handleTx forwards an already signed transaction to the node. Proxying rather
// than pointing the browser at the RPC port keeps everything same-origin.
func (s *server) handleTx(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, fmt.Errorf("POST only"))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	var req txRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	raw, err := base64.StdEncoding.DecodeString(req.Tx)
	if err != nil {
		httpError(w, http.StatusBadRequest, err)
		return
	}

	res, err := s.cfg.ClientCtx.BroadcastTxSync(raw)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, map[string]any{
		"code":   res.Code,
		"rawLog": res.RawLog,
		"txhash": res.TxHash,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
