# ============================================================
#  Nurvis — Makefile
# ============================================================

# ---------- 版本信息（编译期注入）-------------------------------
# VERSION：优先读 git tag（如 v1.2.3），否则 fallback 到 dev
VERSION   ?= $(shell git describe --tags --always --match "v[0-9]*" 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

MODULE    = github.com/zboya/nurvis
PKG_VER   = $(MODULE)/internal/version

LDFLAGS   = -s -w \
            -X '$(PKG_VER).Version=$(VERSION)' \
            -X '$(PKG_VER).Commit=$(COMMIT)' \
            -X '$(PKG_VER).BuildTime=$(BUILD_TIME)'

# ---------- 产物路径 -------------------------------------------
BIN_DIR   = bin
BINARY    = $(BIN_DIR)/nurvisd
CMD_PATH  = ./cmd/nurvisd

# 桌面应用（Wails3）入口
DESKTOP_BINARY = $(BIN_DIR)/nurvis-desktop
DESKTOP_PATH   = ./cmd/nurvis-desktop

# 参与 vet/test 的包：排除 Wails 脚手架（build/ios|android 等平台打包模板，
# 它们是 package main 但无 func main，仅供 wails 打包时使用）。
PKGS = ./cmd/... ./internal/...

# ---------- Go 工具 -------------------------------------------
GO        ?= go
GOFLAGS   ?=

.PHONY: all build run clean version test vet check lint ci \
        desktop desktop-build desktop-dev desktop-package \
        build-linux build-linux-arm64 build-darwin build-darwin-arm64

# ---------- 默认目标 ------------------------------------------
all: build

# ---------- 构建 ----------------------------------------------
build: $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD_PATH)
	@echo "✓ built $(BINARY)  [$(VERSION) / $(COMMIT)]"

$(BIN_DIR):
	@mkdir -p $(BIN_DIR)

# 交叉编译便捷目标
build-linux: $(BIN_DIR)
	GOOS=linux GOARCH=amd64 \
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
	    -o $(BIN_DIR)/nurvisd-linux-amd64 $(CMD_PATH)

build-linux-arm64: $(BIN_DIR)
	GOOS=linux GOARCH=arm64 \
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
	    -o $(BIN_DIR)/nurvisd-linux-arm64 $(CMD_PATH)

build-darwin: $(BIN_DIR)
	GOOS=darwin GOARCH=amd64 \
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
	    -o $(BIN_DIR)/nurvisd-darwin-amd64 $(CMD_PATH)

build-darwin-arm64: $(BIN_DIR)
	GOOS=darwin GOARCH=arm64 \
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
	    -o $(BIN_DIR)/nurvisd-darwin-arm64 $(CMD_PATH)

# ---------- 桌面应用（Wails3）---------------------------------
# 前置：需安装 wails3 CLI 与 Node 依赖。前端会先被构建到 frontend/dist 再 embed。
desktop: desktop-build

# 直接用 go 构建桌面二进制（需先 cd frontend && npm run build 生成 dist）
desktop-build: $(BIN_DIR)
	cd frontend && npm run build
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(DESKTOP_BINARY) $(DESKTOP_PATH)
	@echo "✓ built $(DESKTOP_BINARY)  [$(VERSION) / $(COMMIT)]"

# 用 wails3 dev 热重载开发（前端改动自动刷新）
desktop-dev:
	wails3 dev

# 用 wails3 打包为平台安装包（.app / .dmg / .deb 等）
desktop-package:
	wails3 package

# ---------- 运行 ----------------------------------------------
run: build
	$(BINARY)

# ---------- 版本信息 ------------------------------------------
version:
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(COMMIT)"
	@echo "Build time: $(BUILD_TIME)"

# ---------- 测试 ----------------------------------------------
test:
	$(GO) test -race -timeout=5m $(PKGS)

test-short:
	$(GO) test -short -timeout=60s $(PKGS)

# ---------- 代码检查 ------------------------------------------
vet:
	$(GO) vet $(PKGS)

lint:
	@which golangci-lint > /dev/null 2>&1 || \
	    (echo "golangci-lint not found, run: brew install golangci-lint" && exit 1)
	golangci-lint run $(PKGS)

check: vet test

# ---------- CI 流程 ------------------------------------------
ci: vet test build

# ---------- 清理 ----------------------------------------------
clean:
	rm -rf $(BIN_DIR)

# ---------- 依赖 ----------------------------------------------
deps:
	$(GO) mod download
	$(GO) mod tidy
