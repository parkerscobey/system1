package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouterConfig configures the OpenRouter provider.
type OpenRouterConfig struct {
	APIKey  string
	Model   string
	BaseURL string
	AppName string
	SiteURL string
	Timeout time.Duration

	// HTTPClient can be injected for tests. If nil, a default client is created.
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// OpenRouterProvider implements Provider using the OpenRouter chat completions API.
type OpenRouterProvider struct {
	config OpenRouterConfig
	client *http.Client
	logger *slog.Logger
}

type openRouterChatCompletionRequest struct {
	Model       string              `json:"model"`
	Messages    []openRouterMessage `json:"messages"`
	Temperature float64             `json:"temperature"`
	MaxTokens   int                 `json:"max_tokens"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterChatCompletionResponse struct {
	Model   string             `json:"model"`
	Choices []openRouterChoice `json:"choices"`
	Usage   openRouterUsage    `json:"usage"`
}

type openRouterChoice struct {
	Message openRouterAssistantMessage `json:"message"`
}

type openRouterAssistantMessage struct {
	Content json.RawMessage `json:"content"`
}

type openRouterUsage struct {
	TotalTokens int `json:"total_tokens"`
}

type openRouterErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// NewOpenRouterProvider creates a new OpenRouter provider.
func NewOpenRouterProvider(config OpenRouterConfig) *OpenRouterProvider {
	if config.BaseURL == "" {
		config.BaseURL = defaultOpenRouterBaseURL
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}

	return &OpenRouterProvider{
		config: config,
		client: client,
		logger: config.Logger,
	}
}

// Name returns the provider name.
func (p *OpenRouterProvider) Name() string {
	return "openrouter"
}

// Health checks whether the provider can reach OpenRouter and authenticate.
func (p *OpenRouterProvider) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.BaseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("build openrouter health request: %w", err)
	}

	p.applyHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openrouter health check request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openrouter health check failed: %s", formatOpenRouterHTTPError(resp.StatusCode, body))
	}

	return nil
}

// Complete generates a response using OpenRouter chat completions.
func (p *OpenRouterProvider) Complete(ctx context.Context, prompt string, systemPrompt string, opts ...Option) (Response, error) {
	start := time.Now()
	options := applyOptions(opts)

	modelName := p.config.Model
	if options.model != "" {
		modelName = options.model
	}
	if strings.TrimSpace(modelName) == "" {
		return Response{}, fmt.Errorf("openrouter model is required")
	}

	messages := buildOpenRouterMessages(prompt, systemPrompt, options)

	requestPayload := openRouterChatCompletionRequest{
		Model:       modelName,
		Messages:    messages,
		Temperature: options.temperature,
		MaxTokens:   options.maxTokens,
	}

	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return Response{}, fmt.Errorf("marshal openrouter request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("build openrouter request: %w", err)
	}
	p.applyHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("openrouter request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Response{}, fmt.Errorf("read openrouter response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("%s", formatOpenRouterHTTPError(resp.StatusCode, body))
	}

	var apiResp openRouterChatCompletionResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return Response{}, fmt.Errorf("decode openrouter response: %w", err)
	}
	if len(apiResp.Choices) == 0 {
		return Response{}, fmt.Errorf("malformed openrouter response: missing choices")
	}

	responseText, err := parseOpenRouterContent(apiResp.Choices[0].Message.Content)
	if err != nil {
		return Response{}, fmt.Errorf("malformed openrouter response: %w", err)
	}

	duration := time.Since(start)
	responseModel := apiResp.Model
	if responseModel == "" {
		responseModel = modelName
	}

	response := Response{
		Text: responseText,
		Metadata: ResponseMetadata{
			Provider:   "openrouter",
			Model:      responseModel,
			TokensUsed: apiResp.Usage.TotalTokens,
			Duration:   duration.String(),
		},
	}

	if options.structured {
		var structured json.RawMessage
		if err := json.Unmarshal([]byte(responseText), &structured); err != nil {
			p.logger.WarnContext(ctx, "failed to parse structured openrouter response",
				slog.String("error", err.Error()),
				slog.String("response", responseText))
		} else {
			response.Structured = structured
		}
	}

	return response, nil
}

func (p *OpenRouterProvider) applyHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	if p.config.AppName != "" {
		req.Header.Set("X-Title", p.config.AppName)
	}
	if p.config.SiteURL != "" {
		req.Header.Set("HTTP-Referer", p.config.SiteURL)
	}
}

func buildOpenRouterMessages(prompt string, systemPrompt string, options options) []openRouterMessage {
	messages := make([]openRouterMessage, 0, 2)

	systemContent := strings.TrimSpace(systemPrompt)
	if options.structured {
		instruction := "Respond with valid JSON only. Output a single JSON value with no markdown, code fences, or explanatory text."
		if options.jsonSchema != "" {
			instruction += " The JSON must conform to this schema: " + options.jsonSchema
		}
		if systemContent == "" {
			systemContent = instruction
		} else {
			systemContent += "\n\nIMPORTANT: " + instruction
		}
	}

	if systemContent != "" {
		messages = append(messages, openRouterMessage{
			Role:    "system",
			Content: systemContent,
		})
	}

	userContent := prompt
	if options.structured {
		userContent += "\n\nReturn only valid JSON."
	}

	messages = append(messages, openRouterMessage{
		Role:    "user",
		Content: userContent,
	})

	return messages
}

func parseOpenRouterContent(content json.RawMessage) (string, error) {
	if len(content) == 0 || string(content) == "null" {
		return "", fmt.Errorf("assistant message content is empty")
	}

	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return "", fmt.Errorf("assistant message content is blank")
		}
		return trimmed, nil
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err == nil {
		var sb strings.Builder
		for _, part := range parts {
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(part.Text)
		}
		trimmed := strings.TrimSpace(sb.String())
		if trimmed == "" {
			return "", fmt.Errorf("assistant message content parts had no text")
		}
		return trimmed, nil
	}

	return "", fmt.Errorf("assistant message content was not a text string")
}

func formatOpenRouterHTTPError(statusCode int, body []byte) string {
	var errResp openRouterErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && strings.TrimSpace(errResp.Error.Message) != "" {
		return fmt.Sprintf("openrouter request failed with status %d: %s", statusCode, strings.TrimSpace(errResp.Error.Message))
	}

	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Sprintf("openrouter request failed with status %d", statusCode)
	}
	if len(message) > 400 {
		message = message[:400] + "..."
	}

	return fmt.Sprintf("openrouter request failed with status %d: %s", statusCode, message)
}
