// Package gateway provides middleware mechanisms: wrapping Handlers to implement
// cross-cutting concerns (logging, auth, tracing, etc.).
package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// Middleware wraps a Handler and returns a new Handler.
// Multiple Middlewares are composed via chain; later-registered ones wrap closer to the real Handler.
type Middleware func(next Handler) Handler

// Use registers one or more middlewares. Subsequently registered handlers via Handle
// will automatically have these middlewares applied.
// Note: Use must be called before Handle; already-registered methods are not retroactively wrapped.
func (s *Server) Use(mws ...Middleware) {
	s.middlewares = append(s.middlewares, mws...)
}

// applyMiddlewares wraps the handler in registration order: first-registered is outermost.
func (s *Server) applyMiddlewares(h Handler) Handler {
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		h = s.middlewares[i](h)
	}
	return h
}

// DebugLogMiddleware prints debug logs for every RPC request and response, including
// method, params, elapsed time, and result or error. Long params/payload are truncated
// to avoid flooding logs.
func DebugLogMiddleware() Middleware {
	const maxLen = 1024 * 10
	return func(next Handler) Handler {
		return func(ctx context.Context, conn *Conn, params json.RawMessage) (any, error) {
			start := time.Now()
			method := methodFromCtx(ctx)
			slog.Debug("gateway: rpc request",
				"conn", conn.ID(),
				"method", method,
				"params", truncate(params, maxLen),
			)

			result, err := next(ctx, conn, params)
			elapsed := time.Since(start)

			if err != nil {
				slog.Debug("gateway: rpc response error",
					"conn", conn.ID(),
					"method", method,
					"elapsed", elapsed,
					"err", err.Error(),
				)
				return result, err
			}

			payload, _ := json.Marshal(result)
			slog.Debug("gateway: rpc response ok",
				"conn", conn.ID(),
				"method", method,
				"elapsed", elapsed,
				"payload", truncate(payload, maxLen),
			)
			return result, err
		}
	}
}

// truncate converts raw JSON to a string and truncates it to the given limit.
func truncate(raw []byte, max int) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "...(truncated)"
}

// methodCtxKey carries the method name in ctx for middleware access.
type methodCtxKey struct{}

func ctxWithMethod(ctx context.Context, method string) context.Context {
	return context.WithValue(ctx, methodCtxKey{}, method)
}

func methodFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(methodCtxKey{}).(string)
	return v
}
