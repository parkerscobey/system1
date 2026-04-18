package model

import (
	"context"
	"encoding/json"
)

// Provider defines the interface for model providers that can generate responses
// from prompts. This abstraction allows System-1 to use different model providers
// for extraction, introspection synthesis, and Waking Mind generation.
type Provider interface {
	// Complete generates a response to the given prompt with optional system prompt.
	// The response includes both text output and optionally structured JSON output.
	Complete(ctx context.Context, prompt string, systemPrompt string, opts ...Option) (Response, error)

	// Name returns the name of the provider (e.g., "oracle", "openai")
	Name() string

	// Health performs a health check on the provider
	Health(ctx context.Context) error
}

// Response represents the output from a model provider.
type Response struct {
	// Text is the raw text response from the model
	Text string `json:"text"`

	// Structured contains parsed structured output if requested and available
	Structured json.RawMessage `json:"structured,omitempty"`

	// Metadata contains additional information about the response
	Metadata ResponseMetadata `json:"metadata"`
}

// ResponseMetadata contains additional information about the model response.
type ResponseMetadata struct {
	// Provider is the name of the provider that generated the response
	Provider string `json:"provider"`

	// Model is the specific model used (if available)
	Model string `json:"model,omitempty"`

	// TokensUsed is the number of tokens consumed (if available)
	TokensUsed int `json:"tokens_used,omitempty"`

	// Duration is how long the request took
	Duration string `json:"duration,omitempty"`
}

// Option configures a Complete request.
type Option interface {
	apply(*options)
}

type options struct {
	structured     bool
	model          string
	temperature    float64
	temperatureSet bool
	maxTokens      int
	maxTokensSet   bool
	jsonSchema     string
}

type optionFunc func(*options)

func (f optionFunc) apply(o *options) {
	f(o)
}

// WithStructuredOutput requests structured JSON output from the model.
// The model response text should be parseable as JSON.
func WithStructuredOutput() Option {
	return optionFunc(func(o *options) {
		o.structured = true
	})
}

// WithModel specifies which model to use (overrides provider default).
func WithModel(model string) Option {
	return optionFunc(func(o *options) {
		o.model = model
	})
}

// WithTemperature sets the temperature for response generation.
func WithTemperature(temp float64) Option {
	return optionFunc(func(o *options) {
		o.temperature = temp
		o.temperatureSet = true
	})
}

// WithMaxTokens sets the maximum number of tokens in the response.
func WithMaxTokens(tokens int) Option {
	return optionFunc(func(o *options) {
		o.maxTokens = tokens
		o.maxTokensSet = true
	})
}

// WithJSONSchema provides a JSON schema for structured output validation.
func WithJSONSchema(schema string) Option {
	return optionFunc(func(o *options) {
		o.jsonSchema = schema
	})
}

// applyOptions applies all options to the options struct.
func applyOptions(opts []Option) options {
	o := options{
		temperature: 0.7,  // default temperature
		maxTokens:   4000, // default max tokens
	}
	for _, opt := range opts {
		opt.apply(&o)
	}
	return o
}
