package model

import (
	"context"
	"encoding/json"
	"testing"
)

// MockProvider is defined in mock.go for shared use across packages

func TestOptions(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		expected options
	}{
		{
			name: "default options",
			opts: []Option{},
			expected: options{
				structured:     false,
				model:          "",
				temperature:    0.7,
				temperatureSet: false,
				maxTokens:      4000,
				maxTokensSet:   false,
				jsonSchema:     "",
			},
		},
		{
			name: "structured output",
			opts: []Option{WithStructuredOutput()},
			expected: options{
				structured:     true,
				model:          "",
				temperature:    0.7,
				temperatureSet: false,
				maxTokens:      4000,
				maxTokensSet:   false,
				jsonSchema:     "",
			},
		},
		{
			name: "custom model",
			opts: []Option{WithModel("gpt-4")},
			expected: options{
				structured:     false,
				model:          "gpt-4",
				temperature:    0.7,
				temperatureSet: false,
				maxTokens:      4000,
				maxTokensSet:   false,
				jsonSchema:     "",
			},
		},
		{
			name: "custom temperature",
			opts: []Option{WithTemperature(0.5)},
			expected: options{
				structured:     false,
				model:          "",
				temperature:    0.5,
				temperatureSet: true,
				maxTokens:      4000,
				maxTokensSet:   false,
				jsonSchema:     "",
			},
		},
		{
			name: "custom max tokens",
			opts: []Option{WithMaxTokens(2000)},
			expected: options{
				structured:     false,
				model:          "",
				temperature:    0.7,
				temperatureSet: false,
				maxTokens:      2000,
				maxTokensSet:   true,
				jsonSchema:     "",
			},
		},
		{
			name: "json schema",
			opts: []Option{WithJSONSchema(`{"type": "object"}`)},
			expected: options{
				structured:     false,
				model:          "",
				temperature:    0.7,
				temperatureSet: false,
				maxTokens:      4000,
				maxTokensSet:   false,
				jsonSchema:     `{"type": "object"}`,
			},
		},
		{
			name: "all options combined",
			opts: []Option{
				WithStructuredOutput(),
				WithModel("gpt-4"),
				WithTemperature(0.8),
				WithMaxTokens(1000),
				WithJSONSchema(`{"type": "array"}`),
			},
			expected: options{
				structured:     true,
				model:          "gpt-4",
				temperature:    0.8,
				temperatureSet: true,
				maxTokens:      1000,
				maxTokensSet:   true,
				jsonSchema:     `{"type": "array"}`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := applyOptions(tt.opts)
			if actual != tt.expected {
				t.Errorf("applyOptions() = %+v, expected %+v", actual, tt.expected)
			}
		})
	}
}

func TestMockProvider(t *testing.T) {
	ctx := context.Background()

	t.Run("name", func(t *testing.T) {
		provider := NewMockProvider("test-provider")
		if provider.Name() != "test-provider" {
			t.Errorf("Name() = %q, expected %q", provider.Name(), "test-provider")
		}
	})

	t.Run("health success", func(t *testing.T) {
		provider := NewMockProvider("test")
		if err := provider.Health(ctx); err != nil {
			t.Errorf("Health() returned error: %v", err)
		}
	})

	t.Run("health failure", func(t *testing.T) {
		provider := NewMockProvider("test")
		expectedErr := context.DeadlineExceeded
		provider.SetHealthError(expectedErr)

		if err := provider.Health(ctx); err != expectedErr {
			t.Errorf("Health() = %v, expected %v", err, expectedErr)
		}
	})

	t.Run("complete default response", func(t *testing.T) {
		provider := NewMockProvider("test")

		resp, err := provider.Complete(ctx, "test prompt", "test system", WithModel("test-model"))
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if resp.Text != "mock response" {
			t.Errorf("Complete() response text = %q, expected %q", resp.Text, "mock response")
		}

		if resp.Metadata.Provider != "test" {
			t.Errorf("Complete() response provider = %q, expected %q", resp.Metadata.Provider, "test")
		}
	})

	t.Run("complete custom response", func(t *testing.T) {
		provider := NewMockProvider("test")
		expectedResp := Response{
			Text:       "custom response",
			Structured: json.RawMessage(`{"key": "value"}`),
			Metadata: ResponseMetadata{
				Provider:   "test",
				Model:      "custom-model",
				TokensUsed: 100,
				Duration:   "200ms",
			},
		}
		provider.AddResponse(expectedResp)

		resp, err := provider.Complete(ctx, "test prompt", "")
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if resp.Text != expectedResp.Text {
			t.Errorf("Complete() response text = %q, expected %q", resp.Text, expectedResp.Text)
		}

		if string(resp.Structured) != string(expectedResp.Structured) {
			t.Errorf("Complete() structured = %q, expected %q", string(resp.Structured), string(expectedResp.Structured))
		}
	})

	t.Run("complete error", func(t *testing.T) {
		provider := NewMockProvider("test")
		expectedErr := context.Canceled
		provider.AddError(expectedErr)

		_, err := provider.Complete(ctx, "test prompt", "")
		if err != expectedErr {
			t.Errorf("Complete() error = %v, expected %v", err, expectedErr)
		}
	})

	t.Run("call count tracking", func(t *testing.T) {
		provider := NewMockProvider("test")

		if provider.CallCount() != 0 {
			t.Errorf("Initial CallCount() = %d, expected 0", provider.CallCount())
		}

		_, _ = provider.Complete(ctx, "prompt 1", "")
		if provider.CallCount() != 1 {
			t.Errorf("CallCount() after first call = %d, expected 1", provider.CallCount())
		}

		_, _ = provider.Complete(ctx, "prompt 2", "")
		if provider.CallCount() != 2 {
			t.Errorf("CallCount() after second call = %d, expected 2", provider.CallCount())
		}
	})
}

func TestResponseSerialization(t *testing.T) {
	resp := Response{
		Text:       "test response",
		Structured: json.RawMessage(`{"test":true}`),
		Metadata: ResponseMetadata{
			Provider:   "test-provider",
			Model:      "test-model",
			TokensUsed: 50,
			Duration:   "150ms",
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(resp)
	if err != nil {
		t.Errorf("Failed to marshal response: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled Response
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Errorf("Failed to unmarshal response: %v", err)
	}

	if unmarshaled.Text != resp.Text {
		t.Errorf("Unmarshaled text = %q, expected %q", unmarshaled.Text, resp.Text)
	}

	if string(unmarshaled.Structured) != string(resp.Structured) {
		t.Errorf("Unmarshaled structured = %q, expected %q", string(unmarshaled.Structured), string(resp.Structured))
	}

	if unmarshaled.Metadata.Provider != resp.Metadata.Provider {
		t.Errorf("Unmarshaled provider = %q, expected %q", unmarshaled.Metadata.Provider, resp.Metadata.Provider)
	}
}
