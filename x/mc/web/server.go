// Package web serves a block explorer for the world x/mc is running.
//
// It is a reader and nothing else. It walks the chain a block at a time,
// decodes the transactions it finds, and streams them to a page, so what you
// are looking at is block data rather than a log of what the gateway thinks it
// sent. Playing does not go through here.
package web

import (
	"context"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
)

//go:embed static
var staticFiles embed.FS

// Config is the input to Serve.
type Config struct {
	ClientCtx client.Context
	// Listen is a host:port for the browser.
	Listen string
	// StatePoll is how often the world's tick and commitment are refreshed.
	StatePoll time.Duration
}

type server struct {
	cfg    Config
	walker *walker

	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

// Serve runs the explorer until the context is cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8667"
	}
	if cfg.StatePoll <= 0 {
		cfg.StatePoll = 250 * time.Millisecond
	}

	if cfg.ClientCtx.Client == nil {
		return fmt.Errorf("the explorer needs a node to read blocks from")
	}

	content, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}

	s := &server{
		cfg:     cfg,
		walker:  newWalker(cfg.ClientCtx),
		clients: make(map[chan []byte]struct{}),
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(content)))
	mux.HandleFunc("/api/stream", s.handleStream)
	mux.HandleFunc("/api/head", s.handleHead)
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

	go func() {
		if err := s.walker.run(ctx, func(b BlockView) { s.publish("block", b) }); err != nil {
			fmt.Printf("mc: explorer stopped walking: %v\n", err)
		}
	}()

	go s.pushState(ctx)

	fmt.Printf("mc: explorer at http://%s\n", cfg.Listen)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// publish fans one event out to every open page. A slow browser is skipped
// rather than allowed to hold the walker up.
func (s *server) publish(event string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	frame := []byte("event: " + event + "\ndata: " + string(body) + "\n\n")

	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.clients {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (s *server) pushState(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.StatePoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if state, err := s.walker.state(ctx); err == nil {
			s.publish("state", state)
		}
	}
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := make(chan []byte, 64)

	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-ch:
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleHead gives a page that just loaded something to show without waiting
// for the next block.
func (s *server) handleHead(w http.ResponseWriter, r *http.Request) {
	state, err := s.walker.state(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}

	writeJSON(w, map[string]any{
		"chainId": s.cfg.ClientCtx.ChainID,
		"state":   state,
		// Enough to fill the feed on a fresh page load rather than a couple of
		// seconds of chain.
		"blocks": s.walker.recent(200),
	})
}

// handleTx looks one transaction up by hash.
//
// The walker's own history answers first. Falling back to the node needs the
// transaction indexer, which this chain runs with turned off, so a hash that
// has scrolled out of history says so rather than pretending.
func (s *server) handleTx(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToUpper(strings.TrimPrefix(r.URL.Query().Get("hash"), "0x"))

	if tx, ok := s.walker.tx(hash); ok {
		writeJSON(w, map[string]any{"source": "block data", "tx": tx})
		return
	}

	raw, err := hex.DecodeString(hash)
	if err != nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("hash is not hex"))
		return
	}

	res, err := s.cfg.ClientCtx.Client.Tx(r.Context(), raw, false)
	if err != nil {
		httpError(w, http.StatusNotFound, fmt.Errorf(
			"not in the last %d blocks, and the node's transaction index did not have it: %w", history, err))
		return
	}

	view, err := s.walker.at(r.Context(), res.Height)
	if err != nil {
		httpError(w, http.StatusBadGateway, err)
		return
	}

	for _, tx := range view.Txs {
		if tx.Hash == hash {
			writeJSON(w, map[string]any{"source": "block " + fmt.Sprint(res.Height), "tx": tx})
			return
		}
	}

	httpError(w, http.StatusNotFound, fmt.Errorf("the index pointed at block %d and it was not there", res.Height))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Printf("mc: explorer write: %v\n", err)
	}
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
