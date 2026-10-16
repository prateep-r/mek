package tui

import "github.com/prateep-r/mek/internal/view"

// What the UI shows, as internal/view reads it.
type (
	Context = view.Context
	Target  = view.Target
	Cluster = view.Cluster
	Tunnel  = view.Tunnel
	Source  = view.Source
	Live    = view.Live
)
