package mcp

import (
	"context"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/mark3labs/mcp-go/server"
)

// StreamableHandler returns the Streamable HTTP handler for the MCP endpoint.
// Authentication is applied by the caller (the assembly root), so the handler
// itself is unauthenticated.
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

// RegisterMetadata mounts the OAuth 2.0 Protected Resource Metadata (RFC 9728)
// handlers for the MCP endpoint on mux.
func (s *Server) RegisterMetadata(mux *http.ServeMux) {
	handler := s.metadataHandler()
	mux.HandleFunc(auth.MetadataPath(s.cfg.EndpointPath), handler)
	if bare := server.WellKnownProtectedResourcePath; bare != auth.MetadataPath(s.cfg.EndpointPath) {
		mux.HandleFunc(bare, handler)
	}
}

// metadataHandler serves OAuth 2.0 Protected Resource Metadata so clients can
// discover the authorization server and the scopes they need.
func (s *Server) metadataHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := server.ProtectedResourceMetadataConfig{
			Resource:               auth.ResourceURL(s.cfg.Auth.PublicURL, s.cfg.EndpointPath, r),
			AuthorizationServers:   []string{s.cfg.Auth.Issuer},
			ScopesSupported:        s.cfg.Auth.Scopes,
			BearerMethodsSupported: []string{"header"},
			ResourceName:           s.cfg.Name,
		}
		server.NewProtectedResourceMetadataHandler(cfg).ServeHTTP(w, r)
	}
}
