package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/spf13/cobra"
)

// envBindings maps serve flags to the environment variables that provide their
// fallback values. A flag passed explicitly always wins over the environment.
var envBindings = []struct {
	flag  string
	env   string
	apply func(*config.Config, string)
}{
	{"auth-public-url", "MCP_PUBLIC_URL", func(c *config.Config, v string) { c.Auth.PublicURL = v }},
	{"google-client-id", "GOOGLE_CLIENT_ID", func(c *config.Config, v string) { c.Auth.GoogleClientID = v }},
	{"google-client-secret", "GOOGLE_CLIENT_SECRET", func(c *config.Config, v string) { c.Auth.GoogleClientSecret = v }},
	{"auth-encryption-key", "AUTH_ENCRYPTION_KEY", func(c *config.Config, v string) { c.Auth.EncryptionKey = v }},
}

// applyEnvFile loads KEY=VALUE pairs from a .env-style file into the process
// environment. Variables already present in the real environment are never
// overwritten, so an explicit export keeps precedence over the file.
func applyEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("env file: %w (copy .env.example to .env and fill it in)", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, unquote(strings.TrimSpace(value)))
	}
	return scanner.Err()
}

// applyEnvFallbacks fills configuration fields from the environment for flags
// the user did not set explicitly. Precedence: flag > environment > default.
func applyEnvFallbacks(cmd *cobra.Command, cfg *config.Config) {
	for _, b := range envBindings {
		if cmd.Flags().Changed(b.flag) {
			continue
		}
		if value := os.Getenv(b.env); value != "" {
			b.apply(cfg, value)
		}
	}
}

// unquote strips a matching pair of single or double quotes. Unquoted values
// drop inline comments starting at a " #" separator, mirroring common .env
// semantics.
func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	if index := strings.Index(value, " #"); index >= 0 {
		value = strings.TrimRight(value[:index], " \t")
	}
	return value
}
