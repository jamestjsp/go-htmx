package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxAPIResponseBytes = 8 << 20

type apiClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	timeout    time.Duration
}

type clientError struct {
	kind    string
	message string
	code    int
	cause   error
}

func (e *clientError) Error() string {
	return e.message
}

func (e *clientError) Unwrap() error {
	return e.cause
}

func (e *clientError) ExitCode() int {
	return e.code
}

type apiErrorBody struct {
	Error struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

func newAPIClient(server string, timeout time.Duration) (*apiClient, error) {
	if timeout <= 0 {
		return nil, usagef("processlab: timeout must be positive")
	}
	baseURL, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" ||
		(baseURL.Scheme != "http" && baseURL.Scheme != "https") {
		return nil, usagef("processlab: invalid server URL %q", server)
	}
	return &apiClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
		timeout:    timeout,
	}, nil
}

func (client *apiClient) request(
	ctx context.Context,
	method string,
	path string,
	input any,
	output any,
) error {
	requestURL := *client.baseURL
	requestPath, query, _ := strings.Cut(strings.TrimLeft(path, "/"), "?")
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/api/v1/" + requestPath
	requestURL.RawQuery = query
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode API request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	requestCtx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL.String(), body)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return &clientError{
			kind:    "unreachable",
			code:    3,
			message: fmt.Sprintf("could not reach Process Lab server at %s; run `processlab serve` to start it", client.baseURL),
			cause:   err,
		}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read API response: %w", err)
	}
	if len(responseBody) > maxAPIResponseBytes {
		return fmt.Errorf("API response exceeds the %d-byte limit", maxAPIResponseBytes)
	}
	if response.StatusCode >= http.StatusBadRequest {
		var envelope apiErrorBody
		if err := json.Unmarshal(responseBody, &envelope); err != nil || envelope.Error.Message == "" {
			return &clientError{
				kind:    "internal",
				code:    1,
				message: fmt.Sprintf("Process Lab returned HTTP %d without a valid error envelope", response.StatusCode),
			}
		}
		return &clientError{
			kind:    envelope.Error.Kind,
			code:    1,
			message: envelope.Error.Message,
		}
	}
	if output == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	return nil
}

func clientErrorKind(err error) string {
	var clientErr *clientError
	if errors.As(err, &clientErr) {
		return clientErr.kind
	}
	return "internal"
}
