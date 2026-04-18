package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenRouterProviderCompleteSuccess(t *testing.T) {
	t.Helper()

	var gotReq openRouterChatCompletionRequest
	var gotAuthHeader string
	var gotTitleHeader string
	var gotRefererHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, expected POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, expected /chat/completions", r.URL.Path)
		}

		gotAuthHeader = r.Header.Get("Authorization")
		gotTitleHeader = r.Header.Get("X-Title")
		gotRefererHeader = r.Header.Get("HTTP-Referer")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"openrouter/response-model","choices":[{"message":{"content":"hello from openrouter"}}],"usage":{"total_tokens":42}}`))
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(OpenRouterConfig{
		APIKey:  "test-api-key",
		Model:   "openrouter/default-model",
		BaseURL: server.URL,
		AppName: "system1-tests",
		SiteURL: "https://system1.test",
		Timeout: time.Second,
	})

	resp, err := provider.Complete(
		context.Background(),
		"user prompt",
		"system prompt",
		WithModel("openrouter/override-model"),
		WithTemperature(0.2),
		WithMaxTokens(111),
	)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if gotAuthHeader != "Bearer test-api-key" {
		t.Fatalf("Authorization header = %q, expected %q", gotAuthHeader, "Bearer test-api-key")
	}
	if gotTitleHeader != "system1-tests" {
		t.Fatalf("X-Title header = %q, expected %q", gotTitleHeader, "system1-tests")
	}
	if gotRefererHeader != "https://system1.test" {
		t.Fatalf("HTTP-Referer header = %q, expected %q", gotRefererHeader, "https://system1.test")
	}

	if gotReq.Model != "openrouter/override-model" {
		t.Fatalf("request model = %q, expected override model", gotReq.Model)
	}
	if gotReq.Temperature != 0.2 {
		t.Fatalf("request temperature = %v, expected 0.2", gotReq.Temperature)
	}
	if gotReq.MaxTokens != 111 {
		t.Fatalf("request max_tokens = %d, expected 111", gotReq.MaxTokens)
	}
	if len(gotReq.Messages) != 2 {
		t.Fatalf("request messages len = %d, expected 2", len(gotReq.Messages))
	}
	if gotReq.Messages[0].Role != "system" || gotReq.Messages[0].Content != "system prompt" {
		t.Fatalf("system message = %+v, expected role=system content=system prompt", gotReq.Messages[0])
	}
	if gotReq.Messages[1].Role != "user" || gotReq.Messages[1].Content != "user prompt" {
		t.Fatalf("user message = %+v, expected role=user content=user prompt", gotReq.Messages[1])
	}

	if resp.Text != "hello from openrouter" {
		t.Fatalf("response text = %q, expected %q", resp.Text, "hello from openrouter")
	}
	if resp.Metadata.Provider != "openrouter" {
		t.Fatalf("response provider = %q, expected openrouter", resp.Metadata.Provider)
	}
	if resp.Metadata.Model != "openrouter/response-model" {
		t.Fatalf("response model = %q, expected openrouter/response-model", resp.Metadata.Model)
	}
	if resp.Metadata.TokensUsed != 42 {
		t.Fatalf("response tokens = %d, expected 42", resp.Metadata.TokensUsed)
	}
	if resp.Metadata.Duration == "" {
		t.Fatal("response duration should not be empty")
	}
}

func TestOpenRouterProviderStructuredOutput(t *testing.T) {
	var gotReq openRouterChatCompletionRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"answer\":\"ok\"}"}}]}`))
	}))
	defer server.Close()

	provider := NewOpenRouterProvider(OpenRouterConfig{
		APIKey:  "test-api-key",
		Model:   "openrouter/model",
		BaseURL: server.URL,
		Timeout: time.Second,
	})

	resp, err := provider.Complete(
		context.Background(),
		"extract answer",
		"",
		WithStructuredOutput(),
		WithJSONSchema(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
	)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(gotReq.Messages) != 2 {
		t.Fatalf("messages len = %d, expected 2 (system + user)", len(gotReq.Messages))
	}
	if gotReq.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q, expected system", gotReq.Messages[0].Role)
	}
	if !strings.Contains(gotReq.Messages[0].Content, "valid JSON only") {
		t.Fatalf("system instruction missing JSON-only guidance: %q", gotReq.Messages[0].Content)
	}
	if !strings.Contains(gotReq.Messages[0].Content, "schema") {
		t.Fatalf("system instruction missing schema guidance: %q", gotReq.Messages[0].Content)
	}
	if gotReq.Messages[1].Role != "user" {
		t.Fatalf("second message role = %q, expected user", gotReq.Messages[1].Role)
	}
	if !strings.Contains(gotReq.Messages[1].Content, "Return only valid JSON") {
		t.Fatalf("user message missing JSON reminder: %q", gotReq.Messages[1].Content)
	}

	if resp.Text != `{"answer":"ok"}` {
		t.Fatalf("response text = %q, expected JSON string", resp.Text)
	}
	if len(resp.Structured) == 0 {
		t.Fatal("expected structured response to be populated")
	}

	var parsed map[string]string
	if err := json.Unmarshal(resp.Structured, &parsed); err != nil {
		t.Fatalf("unmarshal structured response: %v", err)
	}
	if parsed["answer"] != "ok" {
		t.Fatalf("structured answer = %q, expected ok", parsed["answer"])
	}
}

func TestOpenRouterProviderCompleteErrors(t *testing.T) {
	t.Run("non-200 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
		}))
		defer server.Close()

		provider := NewOpenRouterProvider(OpenRouterConfig{
			APIKey:  "bad-key",
			Model:   "openrouter/model",
			BaseURL: server.URL,
			Timeout: time.Second,
		})

		_, err := provider.Complete(context.Background(), "prompt", "")
		if err == nil {
			t.Fatal("expected error for non-200 response")
		}
		if !strings.Contains(err.Error(), "status 401") {
			t.Fatalf("error = %q, expected status code", err)
		}
		if !strings.Contains(err.Error(), "invalid api key") {
			t.Fatalf("error = %q, expected API error message", err)
		}
	})

	t.Run("malformed response missing choices", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[]}`))
		}))
		defer server.Close()

		provider := NewOpenRouterProvider(OpenRouterConfig{
			APIKey:  "test-key",
			Model:   "openrouter/model",
			BaseURL: server.URL,
			Timeout: time.Second,
		})

		_, err := provider.Complete(context.Background(), "prompt", "")
		if err == nil {
			t.Fatal("expected malformed response error")
		}
		if !strings.Contains(err.Error(), "missing choices") {
			t.Fatalf("error = %q, expected missing choices", err)
		}
	})

	t.Run("malformed response invalid content type", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":{"not":"text"}}}]}`))
		}))
		defer server.Close()

		provider := NewOpenRouterProvider(OpenRouterConfig{
			APIKey:  "test-key",
			Model:   "openrouter/model",
			BaseURL: server.URL,
			Timeout: time.Second,
		})

		_, err := provider.Complete(context.Background(), "prompt", "")
		if err == nil {
			t.Fatal("expected malformed response error")
		}
		if !strings.Contains(err.Error(), "content was not a text string") {
			t.Fatalf("error = %q, expected invalid content type message", err)
		}
	})
}
