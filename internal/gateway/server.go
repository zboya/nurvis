// Package gateway implements a WebSocket JSON-RPC server.
// Frame format: req / res / event, see AGENTS.md §12.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/zboya/nurvis/internal/bus"
)

// Frame is the unified frame for WebSocket transport.
type Frame struct {
	Type    string          `json:"type"`              // req | res | event
	ID      string          `json:"id,omitempty"`      // req/res 使用
	Method  string          `json:"method,omitempty"`  // req 使用
	Params  json.RawMessage `json:"params,omitempty"`  // req 使用
	OK      *bool           `json:"ok,omitempty"`      // res 使用
	Payload json.RawMessage `json:"payload,omitempty"` // res/event 使用
	Event   string          `json:"event,omitempty"`   // event 使用
	Err     *RPCError       `json:"error,omitempty"`   // res 错误
}

// RPCError is the JSON-RPC error structure.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HandlerFunc is the method handler function type.
func (e *RPCError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Handler processes an RPC method call and returns a result or error.
type Handler func(ctx context.Context, conn *Conn, params json.RawMessage) (any, error)

// Server is the WebSocket JSON-RPC server.
type Server struct {
	mux         map[string]Handler
	bus         bus.Bus
	token       string // optional auth token (empty = no validation)
	mu          sync.RWMutex
	conns       map[string]*Conn
	middlewares []Middleware
}

// NewServer creates a Gateway Server.
func NewServer(b bus.Bus, token string) *Server {
	return &Server{
		mux:   make(map[string]Handler),
		bus:   b,
		token: token,
		conns: make(map[string]*Conn),
	}
}

// Handle registers a method handler.
func (s *Server) Handle(method string, h Handler) {
	s.mux[method] = h
}

// ServeHTTP implements http.Handler, upgrading to a WebSocket connection.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // skip Origin check in dev
	})
	if err != nil {
		slog.Warn("gateway: ws accept error", "err", err)
		return
	}

	conn := newConn(wsConn)
	s.mu.Lock()
	s.conns[conn.id] = conn
	s.mu.Unlock()

	slog.Info("gateway: client connected", "conn", conn.id)
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn.id)
		s.mu.Unlock()
		slog.Info("gateway: client disconnected", "conn", conn.id)
	}()

	s.serveConn(r.Context(), conn)
}

func (s *Server) serveConn(ctx context.Context, conn *Conn) {
	// Subscribe to bus events and forward to this connection
	eventCh, unsub := s.bus.Subscribe("*")
	defer unsub()

	go func() {
		for e := range eventCh {
			payload, _ := json.Marshal(e.Data)
			frame := Frame{
				Type:    "event",
				Event:   e.Topic,
				Payload: payload,
			}
			if err := conn.Send(ctx, frame); err != nil {
				return
			}
		}
	}()

	// Read and process request frames
	for {
		var frame Frame
		if err := conn.Recv(ctx, &frame); err != nil {
			if websocket.CloseStatus(err) != -1 {
				return // normal close
			}
			slog.Warn("gateway: recv error", "conn", conn.id, "err", err)
			return
		}

		if frame.Type != "req" {
			continue
		}

		go s.handleReq(ctx, conn, frame)
	}
}

func (s *Server) handleReq(ctx context.Context, conn *Conn, frame Frame) {
	h, ok := s.mux[frame.Method]
	if !ok {
		s.sendError(ctx, conn, frame.ID, "method_not_found",
			fmt.Sprintf("method %q not found", frame.Method))
		return
	}

	// Apply middleware chain and inject method into ctx for middleware access
	wrapped := s.applyMiddlewares(h)
	ctx = ctxWithMethod(ctx, frame.Method)

	result, err := wrapped(ctx, conn, frame.Params)
	if err != nil {
		var rpcErr *RPCError
		if e, ok2 := err.(*RPCError); ok2 {
			rpcErr = e
		} else {
			rpcErr = &RPCError{Code: "internal_error", Message: err.Error()}
		}
		s.sendError(ctx, conn, frame.ID, rpcErr.Code, rpcErr.Message)
		return
	}

	payload, _ := json.Marshal(result)
	t := true
	res := Frame{Type: "res", ID: frame.ID, OK: &t, Payload: payload}
	_ = conn.Send(ctx, res)
}

func (s *Server) sendError(ctx context.Context, conn *Conn, id, code, msg string) {
	f := false
	frame := Frame{
		Type: "res",
		ID:   id,
		OK:   &f,
		Err:  &RPCError{Code: code, Message: msg},
	}
	_ = conn.Send(ctx, frame)
}

// Broadcast sends an event frame to all connections (typically called by bus handler).
func (s *Server) Broadcast(ctx context.Context, event string, data any) {
	payload, _ := json.Marshal(data)
	frame := Frame{Type: "event", Event: event, Payload: payload}
	s.mu.RLock()
	conns := make([]*Conn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.RUnlock()
	for _, c := range conns {
		_ = c.Send(ctx, frame)
	}
}

// --- Conn ---

// Conn wraps a single WebSocket connection.
type Conn struct {
	id   string
	ws   *websocket.Conn
	mu   sync.Mutex
	auth bool // whether connect handshake is completed
}

func newConn(ws *websocket.Conn) *Conn {
	return &Conn{id: uuid.New().String(), ws: ws}
}

// Send serializes and sends a frame.
func (c *Conn) Send(ctx context.Context, frame Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(writeCtx, websocket.MessageText, data)
}

// Recv receives and deserializes a frame.
func (c *Conn) Recv(ctx context.Context, frame *Frame) error {
	_, data, err := c.ws.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, frame)
}

// ID returns the unique connection ID.
func (c *Conn) ID() string { return c.id }
