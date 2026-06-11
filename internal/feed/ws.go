package feed

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSPath is the route under which the WS upgrade handler is mounted.
const WSPath = "/v1/events/subscribe"

// Channel labels the hello frame's stream identity (parity with
// ds-capture's broadcaster channel name).
const Channel = "vendor_round_result"

// WSConfig customises the WebSocket sink.
type WSConfig struct {
	Poller            *Poller
	Auth              AuthConfig
	HeartbeatInterval time.Duration // 0 ⇒ 30s
	WriteTimeout      time.Duration // 0 ⇒ 10s
	MaxConnections    int           // 0 ⇒ unlimited
	CheckOrigin       func(*http.Request) bool
	Logger            *log.Logger
}

type wsHandler struct {
	cfg       WSConfig
	upgrader  websocket.Upgrader
	connectMu sync.Mutex
	conns     map[*websocket.Conn]struct{}
}

// RegisterWS mounts WSPath on mux and returns a connected-count accessor
// (for /v1/readyz / observability).
func RegisterWS(mux *http.ServeMux, cfg WSConfig) (connectedCount func() int) {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.CheckOrigin == nil {
		cfg.CheckOrigin = func(*http.Request) bool { return true }
	}
	h := &wsHandler{
		cfg:      cfg,
		upgrader: websocket.Upgrader{CheckOrigin: cfg.CheckOrigin},
		conns:    make(map[*websocket.Conn]struct{}),
	}
	mux.HandleFunc(WSPath, h.serve)
	return h.connected
}

func (h *wsHandler) connected() int {
	h.connectMu.Lock()
	defer h.connectMu.Unlock()
	return len(h.conns)
}

// serve handles one WS upgrade: auth → conn cap → upgrade → hello →
// subscribe → stream + heartbeat. Frames are newline-free text JSON,
// matching ds-capture's contract (hello / heartbeat / race envelope).
func (h *wsHandler) serve(w http.ResponseWriter, r *http.Request) {
	// Auth runs BEFORE the connection cap so unauthorised floods can't
	// exhaust MaxConnections.
	if !h.cfg.Auth.authorised(r) {
		writeAuthError(w, r, h.cfg.Logger)
		return
	}

	if h.cfg.MaxConnections > 0 && h.connected() >= h.cfg.MaxConnections {
		http.Error(w, "ws: too many connections", http.StatusServiceUnavailable)
		return
	}

	gameTypeFilter := strings.TrimSpace(r.URL.Query().Get("gameType"))

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // upgrader writes its own error response
	}

	h.connectMu.Lock()
	h.conns[conn] = struct{}{}
	h.connectMu.Unlock()
	defer func() {
		h.connectMu.Lock()
		delete(h.conns, conn)
		h.connectMu.Unlock()
		_ = conn.Close()
	}()

	ctx := r.Context()

	if err := h.writeJSON(conn, map[string]any{
		"type":       "hello",
		"serverTime": time.Now().UTC().Format(time.RFC3339Nano),
		"channel":    Channel,
	}); err != nil {
		return
	}

	ch, unsub := h.cfg.Poller.Subscribe()
	defer unsub()

	heartbeat := time.NewTicker(h.cfg.HeartbeatInterval)
	defer heartbeat.Stop()

	// Reader goroutine drains control frames so the conn doesn't wedge on
	// the peer's close/ping.
	readerErr := make(chan error, 1)
	go func() {
		for {
			if _, _, rerr := conn.NextReader(); rerr != nil {
				readerErr <- rerr
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-readerErr:
			return
		case ev, ok := <-ch:
			if !ok {
				return // poller shutting down
			}
			if gameTypeFilter != "" && ev.GameType != gameTypeFilter {
				continue
			}
			if err := h.writeEvent(conn, ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := h.writeJSON(conn, map[string]any{
				"type": "heartbeat",
				"ts":   time.Now().UTC().Format(time.RFC3339Nano),
			}); err != nil {
				return
			}
		}
	}
}

// writeEvent serialises one LiveResultDTO as a race envelope.
func (h *wsHandler) writeEvent(conn *websocket.Conn, ev LiveResultDTO) error {
	return h.writeJSON(conn, map[string]any{
		"type":    eventType(ev),
		"eventId": ev.EventID,
		"data":    ev,
	})
}

func (h *wsHandler) writeJSON(conn *websocket.Conn, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, raw)
}

// eventType labels the frame for client-side routing.
func eventType(ev LiveResultDTO) string {
	switch ev.State {
	case "open", "live":
		return "open"
	case "final":
		return "final"
	default:
		return "race"
	}
}
