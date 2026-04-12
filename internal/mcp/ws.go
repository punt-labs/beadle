package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/server"
)

const wsReadLimit = 16 * 1024 * 1024 // 16 MB

// WSServer serves MCP sessions over WebSocket at /mcp and a health
// endpoint at /health. Each WebSocket connection gets its own MCP
// session bridged via io.Pipe.
type WSServer struct {
	mcp      *server.MCPServer
	version  string
	logger   *slog.Logger
	upgrader websocket.Upgrader
}

// NewWSServer creates a WebSocket server that bridges connections to
// the given MCP server.
func NewWSServer(s *server.MCPServer, version string, logger *slog.Logger) *WSServer {
	return &WSServer{
		mcp:     s,
		version: version,
		logger:  logger,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// ListenAndServe starts the HTTP server on the given port. It blocks
// until the context is canceled, then shuts down gracefully.
func (ws *WSServer) ListenAndServe(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", ws.HandleMCP)
	mux.HandleFunc("/health", ws.HandleHealth)

	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	srv := &http.Server{Addr: addr, Handler: mux}

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
func (ws *WSServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": ws.version,
	})
}

// HandleMCP upgrades the connection to WebSocket and bridges it to an
// MCP stdio session via a pair of pipes.
func (ws *WSServer) HandleMCP(w http.ResponseWriter, r *http.Request) {
	conn, err := ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		ws.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(wsReadLimit)
	defer conn.Close()

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
		defer clientWriter.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			// StdioServer expects newline-delimited JSON.
			msg = append(msg, '\n')
			if _, err := clientWriter.Write(msg); err != nil {
				cancel()
				return
			}
		}
	}()

	// Goroutine 2: read MCP responses from serverReader, write to WebSocket.
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 64*1024)
		for {
			n, err := serverReader.Read(buf)
			if err != nil {
				cancel()
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
				cancel()
				return
			}
		}
	}()

	// Run the MCP session. Listen blocks until EOF or context cancel.
	if err := stdio.Listen(ctx, clientReader, serverWriter); err != nil {
		ws.logger.Debug("mcp session ended", "error", err)
	}

	// Close the server writer so goroutine 2 sees EOF.
	serverWriter.Close()
	cancel()
	wg.Wait()

	ws.logger.Info("websocket session ended", "remote", conn.RemoteAddr())
}
