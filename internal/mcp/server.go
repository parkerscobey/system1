package mcp

import "context"

// Server will expose the MCP-facing System-1 interface.
// MVP target surface:
// - start_session
// - introspect
// - end_session
type Server struct{}

func New() *Server { return &Server{} }

func (s *Server) Start(context.Context) error { return nil }
