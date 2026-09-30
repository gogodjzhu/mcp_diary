package auth

import (
	"net/http"
	"path"
	"strings"
)

// BaseURL returns the externally reachable base URL of the server. When
// publicURL is set it is used verbatim; otherwise it is derived from the
// request, honouring the common reverse-proxy headers.
func BaseURL(publicURL string, r *http.Request) string {
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}
	if r == nil {
		return ""
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}

	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}

	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// ResourceURL returns the canonical identifier of the protected resource, i.e.
// the externally reachable MCP endpoint.
func ResourceURL(publicURL, endpointPath string, r *http.Request) string {
	base := BaseURL(publicURL, r)
	if base == "" {
		return ""
	}
	return base + normalizePath(endpointPath)
}

// MetadataPath returns the RFC 9728 well-known path for a resource whose
// endpoint is endpointPath.
func MetadataPath(endpointPath string) string {
	p := strings.Trim(normalizePath(endpointPath), "/")
	if p == "" {
		return "/.well-known/oauth-protected-resource"
	}
	return path.Join("/.well-known/oauth-protected-resource", p)
}

// MetadataURL returns the absolute URL of the protected resource metadata
// document advertised in WWW-Authenticate challenges.
func MetadataURL(publicURL, endpointPath string, r *http.Request) string {
	base := BaseURL(publicURL, r)
	if base == "" {
		return ""
	}
	return base + MetadataPath(endpointPath)
}

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
