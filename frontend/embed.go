// Package frontend 内嵌前端构建产物，供桌面应用（Wails）加载。
// 运行前需先执行：cd frontend && npm run build。
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist 返回 dist 目录的子文件系统（去掉 dist 前缀，根即 index.html）。
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("frontend: embed dist sub fs: " + err.Error())
	}
	return sub
}
