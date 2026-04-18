package model

import "context"

// MockProvider implements Provider for testing.
// It can be used by other packages to test model-dependent functionality.
type MockProvider struct {
	name        string
	responses   []Response
	errors      []error
	callCount   int
	healthError error
}

// NewMockProvider creates a new MockProvider with the given name.
func NewMockProvider(name string) *MockProvider {
	return &MockProvider{
		name:      name,
		responses: []Response{},
		errors:    []error{},
	}
}

// Name returns the provider name.
func (m *MockProvider) Name() string {
	return m.name
}

// Health returns any configured health error or nil.
func (m *MockProvider) Health(ctx context.Context) error {
	return m.healthError
}

// Complete returns the next configured response or error.
// If no responses/errors are configured, returns a default response.
func (m *MockProvider) Complete(ctx context.Context, prompt string, systemPrompt string, opts ...Option) (Response, error) {
	defer func() { m.callCount++ }()

	if m.callCount < len(m.errors) && m.errors[m.callCount] != nil {
		return Response{}, m.errors[m.callCount]
	}

	if m.callCount < len(m.responses) {
		return m.responses[m.callCount], nil
	}

	// Default response
	return Response{
		Text: "mock response",
		Metadata: ResponseMetadata{
			Provider: m.name,
			Duration: "100ms",
		},
	}, nil
}

// AddResponse adds a response to be returned on the next Complete call.
func (m *MockProvider) AddResponse(response Response) {
	m.responses = append(m.responses, response)
}

// AddError adds an error to be returned on the next Complete call.
func (m *MockProvider) AddError(err error) {
	m.errors = append(m.errors, err)
}

// SetHealthError sets the error to be returned by Health.
func (m *MockProvider) SetHealthError(err error) {
	m.healthError = err
}

// CallCount returns the number of Complete calls made.
func (m *MockProvider) CallCount() int {
	return m.callCount
}
