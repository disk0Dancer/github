package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the default server URL.
const DefaultBaseURL = "https://api.github.com"

// Client is an HTTP API client.
type Client struct {
	BaseURL    string
	Headers    map[string]string
	HTTPClient *http.Client
}

// Response holds an API response.
type Response struct {
	StatusCode int
	Body       string
	Raw        interface{}
}

// NewClient creates a new Client.
func NewClient(baseURL string, headers map[string]string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Headers:    headers,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Do executes an HTTP request.
func (c *Client) Do(method, path string, query map[string]string, body []byte, extraHeaders ...map[string]string) (*Response, error) {
	fullURL := c.BaseURL + path
	if len(query) > 0 {
		params := url.Values{}
		for k, v := range query {
			params.Set(k, v)
		}
		fullURL += "?" + params.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	for _, eh := range extraHeaders {
		for k, v := range eh {
			req.Header.Set(k, v)
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var raw interface{}
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &raw)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Raw:        raw,
	}, nil
}
