package ingest

// Package ingest owns raw substrate reading, cursoring, and turn-span building.
//
// Current runtime behavior uses one active source at a time with auto-discovery:
// - explicit SYSTEM1_SESSION_LOG_PATH (JSONL)
// - default ~/.system1/sessions.jsonl
// - OpenCode JSONL discovery
// - OpenCode SQLite discovery (normalized into a local mirror JSONL)
