package main

import (
	"embed"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Create an instance of the app structure
	app := &App{}

	// Create application with options
	err := wails.Run(&options.App{
		Title:         "krokodyl",
		Width:         800,
		Height:        600,
		MinWidth:      400,
		MinHeight:     500,
		MaxWidth:      1000,
		MaxHeight:     800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		logrus.WithError(err).Fatal("an error occured while running the app")
	}
}
