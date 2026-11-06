package main

import (
	"embed"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"io/fs"
)

//go:embed frontend/index.html frontend/style.css frontend/app.js frontend/model.js frontend/maintenance.js
var assets embed.FS

func main() {
	app := &App{}
	files, err := fs.Sub(assets, "frontend")
	if err != nil {
		panic(err)
	}
	if err = wails.Run(&options.App{Title: "Nekonomicon", Width: 980, Height: 860, MinWidth: 390, MinHeight: 560, AssetServer: &assetserver.Options{Assets: files}, OnStartup: app.startup, OnDomReady: app.domReady, OnBeforeClose: app.beforeClose, OnShutdown: app.shutdown, Bind: []interface{}{app}, BackgroundColour: options.NewRGB(19, 18, 23)}); err != nil {
		println("Nekonomicon could not start:", err.Error())
	}
}
