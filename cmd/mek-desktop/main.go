//go:build desktop

// Command mek-desktop is mek's desktop app: a window over the same
// contexts, tunnels, resources and audit log as mek ui. It binds
// internal/desktop's Service to the window, and runs mek for everything
// that acts. Build it with `make desktop` (it needs the frontend built and
// cgo; on Linux, GTK and WebKitGTK).
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/prateep-r/mek/internal/desktop"
	"github.com/prateep-r/mek/internal/view"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	desktop.UseLoginPath() // started from the Dock: the cloud CLIs are on the login shell's PATH
	self, _ := os.Executable()
	mek, err := desktop.FindMek(self)
	if err != nil {
		log.Print(err)
		mek = "mek" // every action then says it isn't found
	}
	dist, _ := fs.Sub(assets, "frontend/dist")
	app := application.New(application.Options{
		Name:        "mek",
		Description: "Cloud contexts, tunnels, resources and the audit log",
		Services:    []application.Service{application.NewService(desktop.New(view.Live{}, mek))},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(dist)},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "mek",
		Width:     1180,
		Height:    760,
		MinWidth:  860,
		MinHeight: 520,
		URL:       "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
