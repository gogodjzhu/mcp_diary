package mcp

import (
	"context"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/mark3labs/mcp-go/server"
)

// StreamableHandler returns the Streamable HTTP handler for the MCP endpoint.
// Authentication is applied by the caller (the assembly root), so the handler
// itself is unauthenticated. The OAuth protected-resource metadata and
// authorization-server metadata are served by the authorization-server layer.
func (s *Server) StreamableHandler() http.Handler {
	return server.NewStreamableHTTPServer(s.mcp,
		server.WithStateLess(true),
		server.WithEndpointPath(s.cfg.EndpointPath),
		server.WithHeartbeatInterval(s.cfg.HeartbeatInterval),
		server.WithDisableStreaming(s.cfg.StreamingDisabled),
		server.WithDisableLocalhostProtection(s.cfg.AllowRemoteClients),
		server.WithStreamableHTTPLogger(s.logger),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if identity, ok := auth.IdentityFrom(r.Context()); ok {
				return auth.WithIdentity(ctx, identity)
			}
			return ctx
		}),
	)
}
