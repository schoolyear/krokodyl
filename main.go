package main

import (
	"embed"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	closeLog := setupFileLogging()
	defer closeLog()

	// Transfer workers are child processes of this same binary; they must
	// never start the GUI.
	if isWorkerProcess() {
		code := runWorker()
		closeLog()
		os.Exit(code)
	}

	// Create an instance of the app structure
	app := &App{}

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "krokodyl",
		Width:     800,
		Height:    600,
		MinWidth:  320,
		MinHeight: 420,
		// App-owned titlebar on Windows; macOS keeps native traffic lights
		// (hidden-inset) and Linux keeps its native titlebar — frameless
		// support is weakest there.
		Frameless: runtime.GOOS == "windows",
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 16, G: 19, B: 21, A: 1},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Menu:       appMenu(),
		Windows: &windows.Options{
			BackdropType: windows.None,
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
			About: &mac.AboutInfo{
				Title:   "krokodyl",
				Message: "Peer-to-peer file transfers powered by croc",
			},
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		logrus.WithError(err).Fatal("an error occured while running the app")
	}
}

// appMenu returns the application menu. macOS needs explicit App and Edit
// menus for the standard shortcuts (Cmd+C/V/X/A/Q) to reach the WKWebView;
// without them paste into input fields is dead. Other platforms get no menu
// bar, matching the previous behavior.
func appMenu() *menu.Menu {
	if runtime.GOOS != "darwin" {
		return nil
	}

	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	m.Append(menu.EditMenu())
	return m
}

// setupFileLogging mirrors logrus output to a log file so errors are
// recoverable when the app is launched from Finder/Explorer without a
// terminal. Logging setup failures are never fatal.
func setupFileLogging() func() {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		logrus.WithError(err).Warn("could not resolve user cache dir, logging to stderr only")
		return func() {}
	}

	logDir := filepath.Join(cacheDir, "krokodyl")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		logrus.WithError(err).Warn("could not create log directory, logging to stderr only")
		return func() {}
	}

	logFile, err := os.OpenFile(filepath.Join(logDir, "krokodyl.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logrus.WithError(err).Warn("could not open log file, logging to stderr only")
		return func() {}
	}

	// Log file first: it must receive the entry even when stderr is dead
	// (GUI-subsystem binaries launched without a console have an invalid
	// stderr handle, and io.MultiWriter stops at the first failing writer).
	logrus.SetOutput(io.MultiWriter(logFile, bestEffortWriter{os.Stderr}))
	logrus.Infof("krokodyl starting, logging to %s", logFile.Name())
	return func() { logFile.Close() }
}

// bestEffortWriter swallows write errors so an optional sink (like a console
// that may not exist) can never break the logging pipeline.
type bestEffortWriter struct {
	w io.Writer
}

func (b bestEffortWriter) Write(p []byte) (int, error) {
	_, _ = b.w.Write(p)
	return len(p), nil
}
