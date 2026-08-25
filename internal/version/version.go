// Package version holds build-time version metadata injected via -ldflags.
package version

import "runtime"

var (
	// Version is the semantic version, e.g. v1.0.0. Injected at build time.
	Version = "dev"
	// Commit is the short git commit hash. Injected at build time.
	Commit = "none"
	// BuildTime is an RFC3339 timestamp. Injected at build time.
	BuildTime = "unknown"
)

// ProtocolVersion is the current panel<->agent WSS message protocol version.
// Bump when adding a new message type. Receivers must tolerate unknown types
// and the panel must stay backward compatible for at least one major version.
//
// v2: adds update_agent/update_res (online agent self-update).
const ProtocolVersion = 2

// MinAgentProtocol is the lowest agent protocol version the panel accepts.
const MinAgentProtocol = 1

// FrpVersion is the pinned frp release used by installers.
const FrpVersion = "0.61.1"

// Info returns a human-readable version string.
func Info() string {
	return Version + " (commit " + Commit + ", built " + BuildTime + ", " + runtime.Version() + ")"
}
