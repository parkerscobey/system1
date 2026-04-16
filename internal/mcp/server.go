package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/session"
)

type Server struct {
	logger         *slog.Logger
	sessionService *session.Service
	introspection  *introspect.Service
}

func New(logger *slog.Logger, sessionService *session.Service, introspection *introspect.Service) *Server {
	return &Server{
		logger:         logger,
		sessionService: sessionService,
		introspection:  introspection,
	}
}

func (s *Server) Start(ctx context.Context) error {
	srv := server.NewMCPServer(
		"System-1",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	srv.AddTool(
		mcp.NewTool(
			"start_session",
			mcp.WithDescription("Start a new session and get the ambient context"),
		),
		s.handleStartSession,
	)

	srv.AddTool(
		mcp.NewTool(
			"introspect",
			mcp.WithDescription("Query the System-1 introspection service"),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("The query to ask the introspection service"),
			),
			mcp.WithBoolean("debug",
				mcp.DefaultBool(false),
				mcp.Description("Include debug information in the response"),
			),
		),
		s.handleIntrospect,
	)

	srv.AddTool(
		mcp.NewTool(
			"end_session",
			mcp.WithDescription("End the current session"),
		),
		s.handleEndSession,
	)

	s.logger.InfoContext(ctx, "MCP server starting on stdio")

	if err := server.ServeStdio(srv); err != nil {
		return fmt.Errorf("MCP server failed: %w", err)
	}

	s.logger.InfoContext(ctx, "MCP server stopped")
	return nil
}

func (s *Server) handleStartSession(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := s.sessionService.Start(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("start session failed: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("%s\n\nAmbient Context IDs: %v", result.WakingMind, result.AmbientContext)), nil
}

func (s *Server) handleIntrospect(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required parameter query: %v", err)), nil
	}

	debug := false
	if debugVal, err := request.RequireBool("debug"); err == nil {
		debug = debugVal
	}

	result, err := s.introspection.Query(ctx, query, debug)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("introspect failed: %v", err)), nil
	}

	var response string
	if debug {
		response = fmt.Sprintf("%s\n\nDebug: artifact_refs=%v evidence=%v", result.Answer, result.ArtifactRefs, result.Evidence)
	} else {
		response = result.Answer
	}

	return mcp.NewToolResultText(response), nil
}

func (s *Server) handleEndSession(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.sessionService.End(ctx); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("end session failed: %v", err)), nil
	}

	return mcp.NewToolResultText("Session ended successfully."), nil
}
