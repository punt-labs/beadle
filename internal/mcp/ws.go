package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/server"
)

const (
	wsReadLimit = 16 * 1024 * 1024 // 16 MB
	tokenBytes  = 32               // 256-bit bearer token
)

// WSServer serves MCP sessions over WebSocket at /mcp and a health
// endpoint at /health. Each WebSocket connection gets its own MCP
// session bridged via io.Pipe.
type WSServer struct {
	mcp      *server.MCPServer
	version  string
	logger   *slog.Logger
	upgrader websocket.Upgrader
	token    string
}

// NewWSServer creates a WebSocket server that bridges connections to
// the given MCP server. It generates a random bearer token that a
// client must present to reach /mcp -- retrieve it via Token() and
// hand it to legitimate clients out of band. CheckOrigin is left at
// the gorilla/websocket default, which rejects a request whose Origin
// header names a different host than the request's own Host header
// and allows requests with no Origin header at all (the case for
// non-browser MCP/CLI clients).
func NewWSServer(s *server.MCPServer, version string, logger *slog.Logger) *WSServer {
	return &WSServer{
		mcp:     s,
		version: version,
		logger:  logger,
		token:   generateToken(),
	}
}

// generateToken returns a random hex-encoded bearer token. A failure to
// read from crypto/rand means the platform's entropy source is broken --
// not a condition this server can recover from or usefully report per
// connection, so it panics once at startup rather than silently minting a
// predictable token.
func generateToken() string {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generate ws auth token: %v", err))
	}
	return hex.EncodeToString(b)
}

// Token returns the bearer token clients must present to connect.
func (ws *WSServer) Token() string {
	return ws.token
}

// ListenAndServe starts the HTTP server on 127.0.0.1:port. It blocks
// until the context is canceled, then shuts down gracefully. Binding to
// loopback only, not 0.0.0.0, is the default for Punt Labs daemons --
// network exposure requires explicit opt-in this server does not offer
// today.
func (ws *WSServer) ListenAndServe(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", ws.HandleMCP)
	mux.HandleFunc("/health", ws.HandleHealth)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	ws.logger.Info("websocket transport listening", "addr", addr)

	select {
	case err := <-errCh:
		return fmt.Errorf("ws server: %w", err)
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	}
}

// HandleHealth responds with a JSON status object.
func (ws *WSServer) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": ws.version,
	}); err != nil {
		ws.logger.Warn("health response write failed", "error", err)
	}
}

// authorized reports whether r carries the server's bearer token, either
// as "Authorization: Bearer <token>" or a "token" query parameter. It uses
// a constant-time comparison so response timing does not leak how much of
// a guessed token matched.
func (ws *WSServer) authorized(r *http.Request) bool {
	presented := r.URL.Query().Get("token")
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		presented = strings.TrimPrefix(auth, "Bearer ")
	}
	if presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(ws.token)) == 1
}

// HandleMCP authenticates the request, then upgrades the connection to
// WebSocket and bridges it to an MCP stdio session via a pair of pipes.
// The bearer token is accepted from an "Authorization: Bearer <token>"
// header or a "token" query parameter, so both a header-capable client
// and a bare WebSocket dialer can authenticate.
func (ws *WSServer) HandleMCP(w http.ResponseWriter, r *http.Request) {
	if !ws.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		ws.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(wsReadLimit)
	defer func() { _ = conn.Close() }()

	ws.logger.Info("websocket session started", "remote", conn.RemoteAddr())

	// Create pipes to bridge WebSocket <-> StdioServer.
	// clientReader/clientWriter: MCP server reads requests from here.
	// serverReader/serverWriter: MCP server writes responses here.
	clientReader, clientWriter := io.Pipe()
	serverReader, serverWriter := io.Pipe()

	stdio := server.NewStdioServer(ws.mcp)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup

	// Goroutine 1: read WebSocket messages, write to clientWriter (MCP stdin).
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { _ = clientWriter.Close() }()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			// StdioServer expects newline-delimited JSON.
			msg = bytes.TrimRight(msg, "\r\n")
			msg = append(msg, '\n')
			if _, err := clientWriter.Write(msg); err != nil {
				cancel()
				return
			}
		}
	}()

	// Goroutine 2: read NDJSON lines from serverReader, write to WebSocket.
	// StdioServer writes newline-delimited JSON; each line is one message.
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(serverReader)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				cancel()
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, line); err != nil {
				cancel()
				return
			}
		}
		cancel()
	}()

	// Run the MCP session. Listen blocks until EOF or context cancel.
	if err := stdio.Listen(ctx, clientReader, serverWriter); err != nil {
		ws.logger.Debug("mcp session ended", "error", err)
	}

	// Close the server writer so goroutine 2 sees EOF.
	_ = serverWriter.Close()
	cancel()
	wg.Wait()

	ws.logger.Info("websocket session ended", "remote", conn.RemoteAddr())
}
