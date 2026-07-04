package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"bypassvpn-windows/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/icons/icon.png
var appIcon []byte

//go:embed assets/icons/tray-icon.png
var trayIcon []byte

//go:embed assets/wintun.dll
var wintunDLL []byte

func main() {
	// Инициализируем wintun DLL (нужен для WireGuard на Windows)
	backend.InitWintun(wintunDLL)

	app := backend.NewApp(trayIcon)

	err := wails.Run(&options.App{
		Title:     "BypassVPN",
		Width:     900,
		Height:    620,
		MinWidth:  800,
		MinHeight: 560,
		Frameless: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 15, B: 20, A: 255},
		OnStartup:        app.Startup,
		OnBeforeClose:    app.OnBeforeClose,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})
	if err != nil {
		panic(err)
	}
}