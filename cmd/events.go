package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	cfg "github/internal/config"
	eventlistener "github/internal/events"
)

var generatedEventDefinitions = []eventlistener.EventDefinition{}

var (
	eventsListenHost               string
	eventsListenPath               string
	eventsListenPort               int
	eventsListenResponseStatus     int
	eventsListenResponseBody       string
	eventsListenTunnel             string
	eventsListenSignatureMode      string
	eventsListenSignatureHeader    string
	eventsListenSigningSecret      string
	eventsListenSignatureAlgorithm string
	eventsListenIncludeTimestamp   bool
	eventsListenTimestampHeader    string
	eventsListenTimestampTolerance int

	eventsEmitTargetURL          string
	eventsEmitMethod             string
	eventsEmitDataJSON           string
	eventsEmitDataFile           string
	eventsEmitSignatureMode      string
	eventsEmitSignatureHeader    string
	eventsEmitSigningSecret      string
	eventsEmitSignatureAlgorithm string
	eventsEmitIncludeTimestamp   bool
	eventsEmitTimestampHeader    string
)

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Inspect, receive, and emit callback/webhook events",
}

var eventsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known callback and webhook event definitions",
	RunE: func(cmd *cobra.Command, args []string) error {
		type listResp struct {
			Events []eventlistener.EventDefinition `json:"events"`
		}
		writeJSON(listResp{Events: generatedEventDefinitions})
		return nil
	},
}

var eventsListenCmd = &cobra.Command{
	Use:   "listen [event-name]",
	Short: "Start a local event listener",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}

		var def eventlistener.EventDefinition
		eventName := ""
		if len(args) == 1 {
			var ok bool
			eventName = args[0]
			def, ok = eventlistener.LookupEvent(generatedEventDefinitions, eventName)
			if !ok {
				return fmt.Errorf("unknown event %q", eventName)
			}
		}

		path := eventsListenPath
		if eventName != "" && !cmd.Flags().Changed("path") {
			path = def.DefaultPath
		}

		allowedMethods := []string(nil)
		if eventName != "" {
			allowedMethods = append(allowedMethods, def.Methods...)
		}

		signatureMode := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-mode"), eventsListenSignatureMode,
			store, "events.signature_mode",
			def.SignatureMode,
			"none",
		)
		signatureHeader := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-header"), eventsListenSignatureHeader,
			store, "events.signature_header",
			def.SignatureHeader,
			"X-Signature",
		)
		signatureAlgorithm := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-algorithm"), eventsListenSignatureAlgorithm,
			store, "events.signature_algorithm",
			def.SignatureAlgorithm,
			"sha256",
		)
		timestampHeader := eventlistener.ResolveString(
			cmd.Flags().Changed("timestamp-header"), eventsListenTimestampHeader,
			store, "events.timestamp_header",
			def.SignatureTimestampHeader,
			"X-Signature-Timestamp",
		)
		includeTimestamp := eventlistener.ResolveBool(
			cmd.Flags().Changed("include-timestamp"), eventsListenIncludeTimestamp,
			store, "events.include_timestamp",
			def.SignatureIncludeTimestamp,
			false,
		)
		tunnel := eventlistener.ResolveString(
			cmd.Flags().Changed("tunnel"), eventsListenTunnel,
			store, "events.tunnel",
			"",
			"none",
		)

		signingSecret := eventsListenSigningSecret
		if signingSecret == "" {
			if value, ok := store.Get("events.signing_secret"); ok {
				signingSecret = value
			}
		}
		if signingSecret == "" && signatureMode != "none" {
			signingSecret = os.Getenv("GITHUB_WEBHOOK_SECRET")
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		listener, err := eventlistener.NewListener(eventlistener.ListenOptions{
			Host:               eventsListenHost,
			Port:               eventsListenPort,
			Path:               path,
			EventName:          eventName,
			AllowedMethods:     allowedMethods,
			ResponseStatus:     eventsListenResponseStatus,
			ResponseBody:       eventsListenResponseBody,
			SignatureMode:      signatureMode,
			SignatureHeader:    signatureHeader,
			SigningSecret:      signingSecret,
			SignatureAlgorithm: signatureAlgorithm,
			IncludeTimestamp:   includeTimestamp,
			TimestampHeader:    timestampHeader,
			TimestampTolerance: time.Duration(eventsListenTimestampTolerance) * time.Second,
		}, emitEventRecord)
		if err != nil {
			return err
		}
		if err := listener.Start(); err != nil {
			return err
		}

		emitEventRecord(eventlistener.StartRecord{
			Type:               "listener.started",
			ListenURL:          listener.ListenURL(),
			Path:               listener.Path(),
			EventName:          eventName,
			Methods:            listener.AllowedMethods(),
			SignatureMode:      signatureMode,
			SignatureHeader:    signatureHeader,
			SignatureAlgorithm: signatureAlgorithm,
			IncludeTimestamp:   includeTimestamp,
		})

		if tunnel != "none" {
			if _, err := eventlistener.StartTunnel(ctx, tunnel, listener.ListenURL(), emitEventRecord); err != nil {
				return err
			}
		}

		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return listener.Shutdown(shutdownCtx)
	},
}

var eventsEmitCmd = &cobra.Command{
	Use:   "emit <event-name>",
	Short: "Emit a synthetic event payload to a target URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := cfg.Load()
		if err != nil {
			return err
		}

		def, ok := eventlistener.LookupEvent(generatedEventDefinitions, args[0])
		if !ok {
			return fmt.Errorf("unknown event %q", args[0])
		}
		if eventsEmitTargetURL == "" {
			return fmt.Errorf("missing required flag --target-url")
		}

		method := eventsEmitMethod
		if method == "" {
			method = def.DefaultMethod
		}

		payload, err := eventlistener.PayloadForEvent(def, eventsEmitDataJSON, eventsEmitDataFile)
		if err != nil {
			return err
		}

		signatureMode := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-mode"), eventsEmitSignatureMode,
			store, "events.signature_mode",
			def.SignatureMode,
			"none",
		)
		signatureHeader := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-header"), eventsEmitSignatureHeader,
			store, "events.signature_header",
			def.SignatureHeader,
			"X-Signature",
		)
		signatureAlgorithm := eventlistener.ResolveString(
			cmd.Flags().Changed("signature-algorithm"), eventsEmitSignatureAlgorithm,
			store, "events.signature_algorithm",
			def.SignatureAlgorithm,
			"sha256",
		)
		timestampHeader := eventlistener.ResolveString(
			cmd.Flags().Changed("timestamp-header"), eventsEmitTimestampHeader,
			store, "events.timestamp_header",
			def.SignatureTimestampHeader,
			"X-Signature-Timestamp",
		)
		includeTimestamp := eventlistener.ResolveBool(
			cmd.Flags().Changed("include-timestamp"), eventsEmitIncludeTimestamp,
			store, "events.include_timestamp",
			def.SignatureIncludeTimestamp,
			false,
		)

		signingSecret := eventsEmitSigningSecret
		if signingSecret == "" {
			if value, ok := store.Get("events.signing_secret"); ok {
				signingSecret = value
			}
		}
		if signingSecret == "" && signatureMode != "none" {
			signingSecret = os.Getenv("GITHUB_WEBHOOK_SECRET")
		}

		headers, err := eventlistener.SignatureHeaders(eventlistener.SignatureOptions{
			Mode:             signatureMode,
			Header:           signatureHeader,
			Secret:           signingSecret,
			Algorithm:        signatureAlgorithm,
			IncludeTimestamp: includeTimestamp,
			TimestampHeader:  timestampHeader,
		}, payload)
		if err != nil {
			return err
		}

		statusCode, err := eventlistener.EmitEvent(eventsEmitTargetURL, method, payload, headers)
		if err != nil {
			return err
		}

		writeJSON(eventlistener.EmitRecord{
			Type:       "listener.emit",
			EventName:  def.Name,
			TargetURL:  eventsEmitTargetURL,
			Method:     method,
			StatusCode: statusCode,
		})
		return nil
	},
}

func emitEventRecord(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error encoding event output:", err)
	}
}

func completeTunnelProviders(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return eventlistener.SupportedTunnelProviders(), cobra.ShellCompDirectiveNoFileComp
}

func completeSignatureModes(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return eventlistener.SupportedSignatureModes(), cobra.ShellCompDirectiveNoFileComp
}

func completeSignatureAlgorithms(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return eventlistener.SupportedSignatureAlgorithms(), cobra.ShellCompDirectiveNoFileComp
}

func completeEventNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return eventlistener.EventNames(generatedEventDefinitions), cobra.ShellCompDirectiveNoFileComp
}

func init() {
	eventsListenCmd.Flags().StringVar(&eventsListenHost, "host", "127.0.0.1", "Host interface to bind the listener to")
	eventsListenCmd.Flags().IntVar(&eventsListenPort, "port", 8081, "Port to listen on")
	eventsListenCmd.Flags().StringVar(&eventsListenPath, "path", "/", "Request path to accept")
	eventsListenCmd.Flags().IntVar(&eventsListenResponseStatus, "response-status", 202, "HTTP status code returned to the sender")
	eventsListenCmd.Flags().StringVar(&eventsListenResponseBody, "response-body", "{\"ok\":true}", "Response body returned to the sender")
	eventsListenCmd.Flags().StringVar(&eventsListenTunnel, "tunnel", "none", "Expose the listener with cloudflared: none|auto|cloudflared")
	eventsListenCmd.Flags().StringVar(&eventsListenSignatureMode, "signature-mode", "", "Verify incoming requests: none|hmac")
	eventsListenCmd.Flags().StringVar(&eventsListenSignatureHeader, "signature-header", "", "Header containing the request signature")
	eventsListenCmd.Flags().StringVar(&eventsListenSigningSecret, "signing-secret", "", "Secret used for signature verification")
	eventsListenCmd.Flags().StringVar(&eventsListenSignatureAlgorithm, "signature-algorithm", "", "HMAC algorithm: sha256|sha1|sha512")
	eventsListenCmd.Flags().BoolVar(&eventsListenIncludeTimestamp, "include-timestamp", false, "Verify signatures over timestamp.body instead of body only")
	eventsListenCmd.Flags().StringVar(&eventsListenTimestampHeader, "timestamp-header", "", "Header containing the signature timestamp")
	eventsListenCmd.Flags().IntVar(&eventsListenTimestampTolerance, "timestamp-tolerance", 300, "Maximum age in seconds for timestamped signatures")
	_ = eventsListenCmd.RegisterFlagCompletionFunc("tunnel", completeTunnelProviders)
	_ = eventsListenCmd.RegisterFlagCompletionFunc("signature-mode", completeSignatureModes)
	_ = eventsListenCmd.RegisterFlagCompletionFunc("signature-algorithm", completeSignatureAlgorithms)

	eventsEmitCmd.Flags().StringVar(&eventsEmitTargetURL, "target-url", "", "Destination URL for the emitted event")
	eventsEmitCmd.Flags().StringVar(&eventsEmitMethod, "method", "", "Override the HTTP method used to emit the event")
	eventsEmitCmd.Flags().StringVar(&eventsEmitDataJSON, "data-json", "", "Inline JSON payload (defaults to the generated sample payload)")
	eventsEmitCmd.Flags().StringVar(&eventsEmitDataFile, "data-file", "", "Path to a JSON payload file")
	eventsEmitCmd.Flags().StringVar(&eventsEmitSignatureMode, "signature-mode", "", "Sign emitted requests: none|hmac")
	eventsEmitCmd.Flags().StringVar(&eventsEmitSignatureHeader, "signature-header", "", "Header used to carry the emitted signature")
	eventsEmitCmd.Flags().StringVar(&eventsEmitSigningSecret, "signing-secret", "", "Secret used for emitted request signatures")
	eventsEmitCmd.Flags().StringVar(&eventsEmitSignatureAlgorithm, "signature-algorithm", "", "HMAC algorithm: sha256|sha1|sha512")
	eventsEmitCmd.Flags().BoolVar(&eventsEmitIncludeTimestamp, "include-timestamp", false, "Sign timestamp.body instead of body only")
	eventsEmitCmd.Flags().StringVar(&eventsEmitTimestampHeader, "timestamp-header", "", "Header used to carry the signing timestamp")
	_ = eventsEmitCmd.RegisterFlagCompletionFunc("signature-mode", completeSignatureModes)
	_ = eventsEmitCmd.RegisterFlagCompletionFunc("signature-algorithm", completeSignatureAlgorithms)

	eventsListenCmd.ValidArgsFunction = completeEventNames
	eventsEmitCmd.ValidArgsFunction = completeEventNames

	eventsCmd.AddCommand(eventsListCmd)
	eventsCmd.AddCommand(eventsListenCmd)
	eventsCmd.AddCommand(eventsEmitCmd)
	rootCmd.AddCommand(eventsCmd)
}
