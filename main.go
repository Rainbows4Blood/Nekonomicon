package main

import (
	"embed"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"io/fs"
	"net/http"
)

//go:embed frontend/index.html frontend/style.css frontend/bootstrap.js frontend/app.js frontend/model.js frontend/maintenance.js frontend/books.js frontend/openai.js frontend/enrichment.js frontend/enrichment-format.js frontend/fantasy.css frontend/assets/*
var assets embed.FS

func main() {
	app := &App{}
	files, err := fs.Sub(assets, "frontend")
	if err != nil {
		panic(err)
	}
	if err = wails.Run(&options.App{Title: "Nekonomicon", Width: 1440, Height: 900, MinWidth: 390, MinHeight: 560, AssetServer: &assetserver.Options{Assets: files, Handler: http.HandlerFunc(app.coverHandler)}, OnStartup: app.startup, OnDomReady: app.domReady, OnBeforeClose: app.beforeClose, OnShutdown: app.shutdown, Bind: []interface{}{app}, BackgroundColour: options.NewRGB(21, 22, 27)}); err != nil {
		println("Nekonomicon could not start:", err.Error())
	}
}
