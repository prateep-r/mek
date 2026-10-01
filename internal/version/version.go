// Package version holds build metadata injected by -ldflags at release time.
package version

// Set by GoReleaser: -X github.com/prateep-r/mek/internal/version.Version=...
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// Repo is the GitHub "owner/name" that releases are published to.
const Repo = "prateep-r/mek"
