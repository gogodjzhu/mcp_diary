package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func enableAuth(c *Config) {
	c.Auth.Enabled = true
	c.Auth.PublicURL = "https://mcp.test"
	c.Auth.GoogleClientID = "google-client"
	c.Auth.GoogleClientSecret = "google-secret"
}

func TestAuthValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "enabled without public url",
			mutate: func(c *Config) {
				c.Auth.Enabled = true
			},
			wantErr: "public URL",
		},
		{
			name: "enabled without google client id",
			mutate: func(c *Config) {
				enableAuth(c)
				c.Auth.GoogleClientID = ""
			},
			wantErr: "google client id",
		},
		{
			name: "enabled without google secret",
			mutate: func(c *Config) {
				enableAuth(c)
				c.Auth.GoogleClientSecret = ""
			},
			wantErr: "google client secret",
		},
		{
			name: "enabled with stdio",
			mutate: func(c *Config) {
				enableAuth(c)
				c.Transport = TransportStdio
			},
			wantErr: "streamable-http",
		},
		{
			name: "valid",
			mutate: func(c *Config) {
				enableAuth(c)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}
