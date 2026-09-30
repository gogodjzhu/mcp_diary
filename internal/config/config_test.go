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

func TestAuthValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "enabled without client id",
			mutate: func(c *Config) {
				c.Auth.Enabled = true
			},
			wantErr: "client id",
		},
		{
			name: "enabled with stdio",
			mutate: func(c *Config) {
				c.Auth.Enabled = true
				c.Auth.ClientID = "id"
				c.Transport = TransportStdio
			},
			wantErr: "streamable-http",
		},
		{
			name: "valid",
			mutate: func(c *Config) {
				c.Auth.Enabled = true
				c.Auth.ClientID = "id"
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

func TestExpectedAudienceFallsBackToClientID(t *testing.T) {
	auth := AuthConfig{ClientID: "abc"}
	if got := auth.ExpectedAudience(); got != "abc" {
		t.Fatalf("audience = %q", got)
	}
	auth.Audience = "xyz"
	if got := auth.ExpectedAudience(); got != "xyz" {
		t.Fatalf("audience = %q", got)
	}
}
