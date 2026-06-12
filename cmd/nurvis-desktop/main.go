// Command nurvis-desktop 是 Nurvis 的 Wails3 桌面应用入口。
//
// 设计：
//   - 后端（app.App，含 Gateway WebSocket）在 Wails Service 生命周期里启停，
//     由 service.ServiceStartup 启动、ServiceShutdown 关闭。
//   - 前端 WebView 通过 Service binding `GetGatewayAddr()` 拿到 Gateway 监听地址，
//     再用 WebSocket JSON-RPC 连接，与渠道/API 完全对等，不开第二套接口。
//
// 参见 AGENTS.md §14（前端）与「统一网关」设计原则。
package main

import (
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/zboya/nurvis/frontend"
)

func main() {
	distFS := frontend.Dist()

	wailsApp := application.New(application.Options{
		Name:        "Nurvis",
		Description: "Nurvis application",
		Services: []application.Service{
			application.NewService(&service{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(distFS),
		},
	})

	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Nurvis",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHidden,
			InvisibleTitleBarHeight: 32,
		},
	})

	if err := wailsApp.Run(); err != nil {
		slog.Error("nurvis-desktop: run error", "err", err)
		os.Exit(1)
	}
}
