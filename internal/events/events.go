package events

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type emitter func(interface{})

// EventDefinition describes a callback or webhook known at generation time.
type EventDefinition struct {
	Name                      string   `json:"name"`
	DisplayName               string   `json:"display_name"`
	Source                    string   `json:"source"`
	Expression                string   `json:"expression,omitempty"`
	DefaultPath               string   `json:"default_path"`
	Methods                   []string `json:"methods"`
	DefaultMethod             string   `json:"default_method"`
	Summary                   string   `json:"summary"`
	Description               string   `json:"description,omitempty"`
	SampleJSON                string   `json:"sample_json,omitempty"`
	SignatureMode             string   `json:"signature_mode,omitempty"`
	SignatureHeader           string   `json:"signature_header,omitempty"`
	SignatureAlgorithm        string   `json:"signature_algorithm,omitempty"`
	SignatureIncludeTimestamp bool     `json:"signature_include_timestamp,omitempty"`
	SignatureTimestampHeader  string   `json:"signature_timestamp_header,omitempty"`
}

// SignatureOptions configures HMAC signing/verification.
type SignatureOptions struct {
	Mode               string
	Header             string
	Secret             string
	Algorithm          string
	IncludeTimestamp   bool
	TimestampHeader    string
	TimestampTolerance time.Duration
}

// ListenOptions configures the local webhook listener.
type ListenOptions struct {
	Host               string
	Port               int
	Path               string
	EventName          string
	AllowedMethods     []string
	ResponseStatus     int
	ResponseBody       string
	SignatureMode      string
	SignatureHeader    string
	SigningSecret      string
	SignatureAlgorithm string
	IncludeTimestamp   bool
	TimestampHeader    string
	TimestampTolerance time.Duration
}

// StartRecord describes a started listener.
type StartRecord struct {
	Type               string   `json:"type"`
	ListenURL          string   `json:"listen_url"`
	Path               string   `json:"path"`
	EventName          string   `json:"event_name,omitempty"`
	Methods            []string `json:"methods,omitempty"`
	SignatureMode      string   `json:"signature_mode,omitempty"`
	SignatureHeader    string   `json:"signature_header,omitempty"`
	SignatureAlgorithm string   `json:"signature_algorithm,omitempty"`
	IncludeTimestamp   bool     `json:"include_timestamp,omitempty"`
}

// TunnelRecord reports a discovered tunnel URL.
type TunnelRecord struct {
	Type      string `json:"type"`
	Provider  string `json:"provider"`
	PublicURL string `json:"public_url"`
	TargetURL string `json:"target_url"`
}

// EmitRecord reports a synthetic emitted event.
type EmitRecord struct {
	Type       string `json:"type"`
	EventName  string `json:"event_name"`
	TargetURL  string `json:"target_url"`
	Method     string `json:"method"`
	StatusCode int    `json:"status_code"`
}

// ErrorRecord reports listener or tunnel failures.
type ErrorRecord struct {
	Type    string `json:"type"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// EventRecord captures one incoming webhook request.
type EventRecord struct {
	Type          string              `json:"type"`
	Name          string              `json:"name,omitempty"`
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	Query         string              `json:"query,omitempty"`
	Headers       map[string][]string `json:"headers,omitempty"`
	Body          interface{}         `json:"body,omitempty"`
	BodyRaw       string              `json:"body_raw,omitempty"`
	Verified      bool                `json:"verified"`
	ReceivedAt    string              `json:"received_at"`
}

// Listener is a local webhook listener.
type Listener struct {
	opts ListenOptions
	emit emitter
	ln   net.Listener
	srv  *http.Server
}

// TunnelSession is a running tunnel process.
type TunnelSession struct {
	Provider string
}

type tunnelProvider struct {
	Name      string
	Binaries  []string
	BuildArgs func(*url.URL) ([]string, error)
}

type tunnelSelection struct {
	Provider string
	Binary   string
	Args     []string
}

var publicURLPattern = regexp.MustCompile(`https?://[^\s\"]+`)

// SupportedTunnelProviders returns all accepted --tunnel values.
func SupportedTunnelProviders() []string {
	return []string{"none", "auto", "cloudflared"}
}

// SupportedSignatureModes returns all accepted signature modes.
func SupportedSignatureModes() []string {
	return []string{"none", "hmac"}
}

// SupportedSignatureAlgorithms returns all accepted HMAC algorithms.
func SupportedSignatureAlgorithms() []string {
	return []string{"sha256", "sha1", "sha512"}
}

// LookupEvent finds a named event definition.
func LookupEvent(defs []EventDefinition, name string) (EventDefinition, bool) {
	for _, def := range defs {
		if def.Name == name {
			return def, true
		}
	}
	return EventDefinition{}, false
}

// EventNames returns all generated event names.
func EventNames(defs []EventDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}

// ResolveString applies flag > config > event default > hard default precedence.
func ResolveString(flagChanged bool, flagValue string, store interface{ Get(string) (string, bool) }, key string, eventDefault string, hardDefault string) string {
	if flagChanged && strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if store != nil {
		if value, ok := store.Get(key); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if strings.TrimSpace(eventDefault) != "" {
		return eventDefault
	}
	return hardDefault
}

// ResolveBool applies flag > config > event default > hard default precedence.
func ResolveBool(flagChanged bool, flagValue bool, store interface{ Get(string) (string, bool) }, key string, eventDefault bool, hardDefault bool) bool {
	if flagChanged {
		return flagValue
	}
	if store != nil {
		if value, ok := store.Get(key); ok {
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err == nil {
				return parsed
			}
		}
	}
	if eventDefault {
		return true
	}
	return hardDefault
}

// PayloadForEvent returns the payload bytes for an event.
func PayloadForEvent(def EventDefinition, dataJSON string, dataFile string) ([]byte, error) {
	switch {
	case strings.TrimSpace(dataJSON) != "":
		return []byte(dataJSON), nil
	case strings.TrimSpace(dataFile) != "":
		return os.ReadFile(dataFile)
	case strings.TrimSpace(def.SampleJSON) != "":
		return []byte(def.SampleJSON), nil
	default:
		return []byte("{}"), nil
	}
}

// SignatureHeaders builds generic HMAC signature headers.
func SignatureHeaders(opts SignatureOptions, payload []byte) (map[string]string, error) {
	mode := normalizeSignatureMode(opts.Mode)
	if mode == "none" {
		return nil, nil
	}
	if strings.TrimSpace(opts.Secret) == "" {
		return nil, errors.New("signing secret is required when signature mode is enabled")
	}

	header := firstNonEmpty(opts.Header, "X-Signature")
	algorithm := normalizeSignatureAlgorithm(opts.Algorithm)
	if algorithm == "" {
		algorithm = "sha256"
	}

	input := payload
	headers := map[string]string{}
	if opts.IncludeTimestamp {
		timestampHeader := firstNonEmpty(opts.TimestampHeader, "X-Signature-Timestamp")
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		headers[timestampHeader] = timestamp
		input = []byte(timestamp + "." + string(payload))
	}

	signature, err := computeHMAC(algorithm, opts.Secret, input)
	if err != nil {
		return nil, err
	}
	headers[header] = signature
	return headers, nil
}

// NewListener creates a local listener.
func NewListener(opts ListenOptions, emit func(interface{})) (*Listener, error) {
	if emit == nil {
		return nil, errors.New("emit callback is required")
	}
	opts.Host = normalizeHost(opts.Host)
	opts.Path = normalizePath(opts.Path)
	opts.SignatureMode = normalizeSignatureMode(opts.SignatureMode)
	opts.SignatureHeader = firstNonEmpty(opts.SignatureHeader, "X-Signature")
	opts.SignatureAlgorithm = firstNonEmpty(normalizeSignatureAlgorithm(opts.SignatureAlgorithm), "sha256")
	opts.TimestampHeader = firstNonEmpty(opts.TimestampHeader, "X-Signature-Timestamp")
	if opts.ResponseStatus == 0 {
		opts.ResponseStatus = http.StatusAccepted
	}
	if opts.ResponseBody == "" {
		opts.ResponseBody = "{\"ok\":true}"
	}
	if opts.TimestampTolerance <= 0 {
		opts.TimestampTolerance = 5 * time.Minute
	}
	if opts.SignatureMode != "none" && strings.TrimSpace(opts.SigningSecret) == "" {
		return nil, errors.New("signing secret is required when signature mode is enabled")
	}

	listener := &Listener{opts: opts, emit: emit}
	listener.srv = &http.Server{
		Handler:           listener.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return listener, nil
}

// Start begins serving requests.
func (l *Listener) Start() error {
	ln, err := net.Listen("tcp", net.JoinHostPort(l.opts.Host, strconv.Itoa(l.opts.Port)))
	if err != nil {
		return fmt.Errorf("starting listener: %w", err)
	}
	l.ln = ln

	go func() {
		if serveErr := l.srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			l.emit(ErrorRecord{Type: "listener.error", Stage: "server", Message: serveErr.Error()})
		}
	}()

	return nil
}

// Shutdown stops the listener.
func (l *Listener) Shutdown(ctx context.Context) error {
	if l.srv == nil {
		return nil
	}
	return l.srv.Shutdown(ctx)
}

// ListenURL returns the local URL for the listener.
func (l *Listener) ListenURL() string {
	if l.ln == nil {
		return ""
	}
	tcpAddr, ok := l.ln.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}
	host := displayHost(l.opts.Host)
	path := l.opts.Path
	if path == "/" {
		path = ""
	}
	return fmt.Sprintf("http://%s:%d%s", host, tcpAddr.Port, path)
}

// Path returns the normalized request path.
func (l *Listener) Path() string {
	return l.opts.Path
}

// AllowedMethods returns the configured allowed methods.
func (l *Listener) AllowedMethods() []string {
	if len(l.opts.AllowedMethods) == 0 {
		return nil
	}
	methods := make([]string, len(l.opts.AllowedMethods))
	copy(methods, l.opts.AllowedMethods)
	return methods
}

func (l *Listener) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.opts.Path != "/" && r.URL.Path != l.opts.Path {
			http.NotFound(w, r)
			return
		}
		if len(l.opts.AllowedMethods) > 0 && !allowsMethod(l.opts.AllowedMethods, r.Method) {
			w.Header().Set("Allow", strings.Join(l.opts.AllowedMethods, ", "))
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			l.emit(ErrorRecord{Type: "listener.error", Stage: "request", Message: fmt.Sprintf("reading request body: %v", err)})
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}

		verified, err := verifySignature(SignatureOptions{
			Mode:               l.opts.SignatureMode,
			Header:             l.opts.SignatureHeader,
			Secret:             l.opts.SigningSecret,
			Algorithm:          l.opts.SignatureAlgorithm,
			IncludeTimestamp:   l.opts.IncludeTimestamp,
			TimestampHeader:    l.opts.TimestampHeader,
			TimestampTolerance: l.opts.TimestampTolerance,
		}, r.Header, bodyBytes)
		if err != nil {
			l.emit(ErrorRecord{Type: "listener.error", Stage: "verification", Message: err.Error()})
			http.Error(w, "signature verification failed", http.StatusUnauthorized)
			return
		}

		decodedBody, bodyRaw := decodeBody(bodyBytes)
		l.emit(EventRecord{
			Type:       "listener.event",
			Name:       l.opts.EventName,
			Method:     r.Method,
			Path:       r.URL.Path,
			Query:      r.URL.RawQuery,
			Headers:    cloneHeaders(r.Header),
			Body:       decodedBody,
			BodyRaw:    bodyRaw,
			Verified:   verified,
			ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		})

		if json.Valid([]byte(l.opts.ResponseBody)) {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(l.opts.ResponseStatus)
		_, _ = io.WriteString(w, l.opts.ResponseBody)
	})
}

func verifySignature(opts SignatureOptions, headers http.Header, payload []byte) (bool, error) {
	mode := normalizeSignatureMode(opts.Mode)
	if mode == "none" {
		return false, nil
	}
	if strings.TrimSpace(opts.Secret) == "" {
		return false, errors.New("signing secret is required when signature mode is enabled")
	}

	header := firstNonEmpty(opts.Header, "X-Signature")
	actual := headers.Get(header)
	if actual == "" {
		return false, fmt.Errorf("%s header is missing", http.CanonicalHeaderKey(header))
	}

	input := payload
	if opts.IncludeTimestamp {
		timestampHeader := firstNonEmpty(opts.TimestampHeader, "X-Signature-Timestamp")
		timestamp := headers.Get(timestampHeader)
		if timestamp == "" {
			return false, fmt.Errorf("%s header is missing", http.CanonicalHeaderKey(timestampHeader))
		}
		if opts.TimestampTolerance > 0 {
			ts, err := strconv.ParseInt(timestamp, 10, 64)
			if err != nil {
				return false, fmt.Errorf("invalid signature timestamp: %w", err)
			}
			requestTime := time.Unix(ts, 0)
			if time.Since(requestTime) > opts.TimestampTolerance || requestTime.Sub(time.Now()) > opts.TimestampTolerance {
				return false, errors.New("signature timestamp is outside the allowed tolerance")
			}
		}
		input = []byte(timestamp + "." + string(payload))
	}

	expected, err := computeHMAC(normalizeSignatureAlgorithm(opts.Algorithm), opts.Secret, input)
	if err != nil {
		return false, err
	}
	if !hmac.Equal([]byte(expected), []byte(actual)) {
		return false, errors.New("signature verification failed")
	}
	return true, nil
}

// EmitEvent sends a JSON payload to a target URL using method.
func EmitEvent(targetURL string, method string, payload []byte, headers map[string]string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(strings.TrimSpace(method)), targetURL, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send event: %w", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// StartTunnel starts a cloudflared tunnel and emits a tunnel record once
// a public URL is detected.
func StartTunnel(ctx context.Context, requested string, targetURL string, emit func(interface{})) (*TunnelSession, error) {
	selection, err := resolveTunnelSelection(requested, targetURL)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, selection.Binary, selection.Args...) //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("preparing %s stdout: %w", selection.Provider, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("preparing %s stderr: %w", selection.Provider, err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s tunnel: %w", selection.Provider, err)
	}

	var once sync.Once
	scan := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			if publicURL := publicURLPattern.FindString(line); publicURL != "" {
				once.Do(func() {
					emit(TunnelRecord{
						Type:      "listener.tunnel",
						Provider:  selection.Provider,
						PublicURL: publicURL,
						TargetURL: targetURL,
					})
				})
			}
		}
	}

	go scan(stdout)
	go scan(stderr)
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil && ctx.Err() == nil {
			emit(ErrorRecord{Type: "listener.error", Stage: "tunnel", Message: fmt.Sprintf("%s tunnel exited: %v", selection.Provider, waitErr)})
		}
	}()

	return &TunnelSession{Provider: selection.Provider}, nil
}

func resolveTunnelSelection(requested string, targetURL string) (tunnelSelection, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return tunnelSelection{}, fmt.Errorf("parsing target URL: %w", err)
	}

	requested = strings.TrimSpace(strings.ToLower(requested))
	if requested == "" || requested == "none" {
		return tunnelSelection{}, errors.New("tunnel provider must not be none")
	}
	if requested == "auto" {
		requested = "cloudflared"
	}
	return providerSelection(requested, target)
}

func providerSelection(name string, target *url.URL) (tunnelSelection, error) {
	if name != "cloudflared" {
		return tunnelSelection{}, fmt.Errorf("unsupported tunnel provider %q", name)
	}

	binary, err := exec.LookPath("cloudflared")
	if err != nil {
		return tunnelSelection{}, errors.New("cloudflared not found in PATH")
	}

	return tunnelSelection{
		Provider: "cloudflared",
		Binary:   binary,
		Args:     []string{"tunnel", "--url", target.String()},
	}, nil
}

func computeHMAC(algorithm string, secret string, payload []byte) (string, error) {
	var factory func() hash.Hash
	switch normalizeSignatureAlgorithm(algorithm) {
	case "sha1":
		factory = sha1.New
	case "sha512":
		factory = sha512.New
	case "", "sha256":
		factory = sha256.New
	default:
		return "", fmt.Errorf("unsupported signature algorithm %q", algorithm)
	}

	mac := hmac.New(factory, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func normalizeSignatureMode(mode string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return "none"
	}
	return mode
}

func normalizeSignatureAlgorithm(algorithm string) string {
	algorithm = strings.TrimSpace(strings.ToLower(algorithm))
	if algorithm == "" {
		return ""
	}
	return algorithm
}

func displayHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}

func allowsMethod(allowed []string, method string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, candidate := range allowed {
		if strings.ToUpper(candidate) == method {
			return true
		}
	}
	return false
}

func decodeBody(body []byte) (interface{}, string) {
	if len(body) == 0 {
		return nil, ""
	}
	var decoded interface{}
	if err := json.Unmarshal(body, &decoded); err == nil {
		return decoded, ""
	}
	return nil, string(body)
}

func cloneHeaders(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		copyValues := make([]string, len(values))
		copy(copyValues, values)
		cloned[key] = copyValues
	}
	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
