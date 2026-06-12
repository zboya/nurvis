package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zboya/nurvis/internal/provider"
)

// HTTPFetch fetches the response content of a given URL (text).
type HTTPFetch struct{}

func (*HTTPFetch) Name() string        { return "web_fetch" }
func (*HTTPFetch) Description() string { return "Fetch the content of a URL and return as text." }
func (*HTTPFetch) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "web_fetch",
		Description: "Fetch the content of a URL (GET request) and return as text. Suitable for APIs, web pages, documentation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to fetch.",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "Optional HTTP headers as key-value pairs.",
				},
				"method": map[string]any{
					"type":        "string",
					"description": "HTTP method: GET (default) or POST.",
					"enum":        []string{"GET", "POST"},
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Request body for POST requests.",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (*HTTPFetch) Invoke(ctx context.Context, raw json.RawMessage, _ Scope) (*Result, error) {
	var args struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	_ = json.Unmarshal(raw, &args)
	if args.URL == "" {
		return &Result{Content: "url is required", IsError: true}, nil
	}
	method := "GET"
	if args.Method != "" {
		method = strings.ToUpper(args.Method)
	}

	var bodyReader io.Reader
	if args.Body != "" {
		bodyReader = strings.NewReader(args.Body)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, args.URL, bodyReader)
	if err != nil {
		return &Result{Content: fmt.Sprintf("invalid request: %v", err), IsError: true}, nil
	}

	// Set custom headers
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &Result{Content: fmt.Sprintf("request error: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	const maxBody = 32 * 1024 // 32KB
	limited := io.LimitReader(resp.Body, maxBody)
	data, err := io.ReadAll(limited)
	if err != nil {
		return &Result{Content: fmt.Sprintf("read body error: %v", err), IsError: true}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP %d\n", resp.StatusCode))
	sb.WriteString(string(data))

	isError := resp.StatusCode >= 400
	return &Result{Content: sb.String(), IsError: isError}, nil
}
