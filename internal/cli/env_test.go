package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/spf13/cobra"
)

func TestApplyEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\n" +
		"\n" +
		"export MCP_PUBLIC_URL=http://localhost:8080\n" +
		"GOOGLE_CLIENT_ID=\"id-1\"\n" +
		"GOOGLE_CLIENT_SECRET='secret-1'\n" +
		"AUTH_ENCRYPTION_KEY=key-1 # inline comment\n" +
		"SYNC_ENCRYPTION_KEY=sync-key-1\n" +
		"not a key value pair\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MCP_PUBLIC_URL", "http://from-real-env") // real environment wins
	// applyEnvFile writes into the shared process environment; restore it.
	for _, b := range envBindings {
		t.Cleanup(func() { _ = os.Unsetenv(b.env) })
	}
	if err := applyEnvFile(path); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		"MCP_PUBLIC_URL":       "http://from-real-env",
		"GOOGLE_CLIENT_ID":     "id-1",
		"GOOGLE_CLIENT_SECRET": "secret-1",
		"AUTH_ENCRYPTION_KEY":  "key-1",
		"SYNC_ENCRYPTION_KEY":  "sync-key-1",
	} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestApplyEnvFileMissing(t *testing.T) {
	if err := applyEnvFile(filepath.Join(t.TempDir(), "absent.env")); err == nil {
		t.Fatal("expected an error for a missing env file")
	}
}

func TestApplyEnvFallbacks(t *testing.T) {
	t.Setenv("MCP_PUBLIC_URL", "http://env-url")
	t.Setenv("GOOGLE_CLIENT_ID", "env-id")
	t.Setenv("SYNC_ENCRYPTION_KEY", "env-sync-key")

	cfg := config.Default()
	cmd := &cobra.Command{Use: "serve"}
	// Bind the flags to the config fields the same way the serve command does.
	cmd.Flags().StringVar(&cfg.Auth.PublicURL, "auth-public-url", cfg.Auth.PublicURL, "")
	cmd.Flags().StringVar(&cfg.Auth.GoogleClientID, "google-client-id", cfg.Auth.GoogleClientID, "")
	cmd.Flags().StringVar(&cfg.Auth.GoogleClientSecret, "google-client-secret", cfg.Auth.GoogleClientSecret, "")
	cmd.Flags().StringVar(&cfg.Auth.EncryptionKey, "auth-encryption-key", cfg.Auth.EncryptionKey, "")
	cmd.Flags().StringVar(&cfg.Sync.EncryptionKey, "sync-encryption-key", cfg.Sync.EncryptionKey, "")
	if err := cmd.Flags().Parse([]string{"--google-client-id=flag-id"}); err != nil {
		t.Fatal(err)
	}

	applyEnvFallbacks(cmd, &cfg)

	if cfg.Auth.PublicURL != "http://env-url" {
		t.Errorf("PublicURL = %q, want the env value", cfg.Auth.PublicURL)
	}
	if cfg.Auth.GoogleClientID != "flag-id" {
		t.Errorf("GoogleClientID = %q, want the explicit flag to win", cfg.Auth.GoogleClientID)
	}
	if cfg.Auth.GoogleClientSecret != "" {
		t.Errorf("GoogleClientSecret = %q, want empty when the env var is unset", cfg.Auth.GoogleClientSecret)
	}
	if cfg.Sync.EncryptionKey != "env-sync-key" {
		t.Errorf("Sync.EncryptionKey = %q, want the env value", cfg.Sync.EncryptionKey)
	}
}
