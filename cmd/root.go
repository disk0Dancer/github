package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cfg "github/internal/config"
)

var (
	outputFormat string
	baseURL      string
)

const defaultBaseURLTemplate = "https://api.github.com"

var version = "1.1.4"

var rootCmd = &cobra.Command{
	Use:     "github",
	Short:   "GitHub's v3 REST API.",
	Version: version,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "json", "Output format: json|table|raw")
	rootCmd.PersistentFlags().StringVar(&baseURL, "base-url", "", "Override API base URL")
}

func getBaseURL() string {
	if baseURL != "" {
		return baseURL
	}
	if v := getConfigValue("core.base_url"); v != "" {
		return v
	}
	if v := os.Getenv("GITHUB_BASE_URL"); v != "" {
		return v
	}
	return resolveDefaultBaseURL()
}

func resolveDefaultBaseURL() string {
	return defaultBaseURLTemplate

}

func getConfigValue(key string) string {
	store, err := cfg.Load()
	if err != nil {
		return ""
	}
	value, ok := store.Get(key)
	if !ok {
		return ""
	}
	return value
}

// getAuthHeaders returns HTTP headers required for authentication.
// Priority: CLI flag -> config -> environment variable -> empty.
func getAuthHeaders() map[string]string {
	headers := map[string]string{}

	return headers
}

// getAuthQueryParams returns query parameters required for authentication
// (used when an API key scheme has in: query).
func getAuthQueryParams() map[string]string {
	params := map[string]string{}

	return params
}

// writeOutput prints v as indented JSON to stdout.
func writeOutput(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error encoding output:", err)
		os.Exit(1)
	}
}

// writeJSON prints v as indented JSON to stdout.
func writeJSON(v interface{}) {
	writeOutput(v)
}

// exitWithError prints an error as JSON to stderr and exits non-zero.
func exitWithError(statusCode int, code, message string, raw interface{}) {
	type errObj struct {
		Status  int         `json:"status"`
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Raw     interface{} `json:"raw,omitempty"`
	}
	type errorWrapper struct {
		Error errObj `json:"error"`
	}
	obj := errorWrapper{Error: errObj{Status: statusCode, Code: code, Message: message, Raw: raw}}
	enc := json.NewEncoder(os.Stderr)
	enc.SetIndent("", "  ")
	_ = enc.Encode(obj)
	os.Exit(1)
}
