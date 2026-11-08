# 分角色发布：瘦包 + 可选全能包
.PHONY: fdt fdt-gw fdt-intranet fdt-atm fdt-atm-gui fdt-portal-gui release windows-atm-gui windows-portal-gui test clean

VERSION ?= 1.0.1
GOFLAGS ?= -trimpath -ldflags="-s -w -X fdt/internal/buildinfo.Version=$(VERSION)"

# Linux Portal 相关目标启用 CGO 以链接 libpam（本机 PAM 身份源）。
# 网关 / ATM / Windows 仍保持 CGO_ENABLED=0 静态构建。
fdt:
	CGO_ENABLED=1 go build $(GOFLAGS) -o bin/fdt ./cmd/fdt

fdt-gw:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/fdt-gw ./cmd/fdt-gw

fdt-intranet:
	CGO_ENABLED=1 go build $(GOFLAGS) -o bin/fdt-intranet ./cmd/fdt-intranet

fdt-atm:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/fdt-atm ./cmd/fdt-atm

# 独立目录 atm-gui/：一键启动 ATM 服务 + 窗口
fdt-atm-gui:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/fdt-atm-gui ./atm-gui

# 独立目录 portal-gui/：一键启动 Portal UI + ATM 数据口 + 首次配置
fdt-portal-gui:
	CGO_ENABLED=1 go build $(GOFLAGS) -o bin/fdt-portal-gui ./portal-gui

windows-atm-gui:
	mkdir -p dist/windows-amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/windows-amd64/fdt-atm-gui.exe ./atm-gui
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/windows-amd64/fdt-atm.exe ./cmd/fdt-atm

windows-portal-gui:
	mkdir -p dist/windows-amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o dist/windows-amd64/fdt-portal-gui.exe ./portal-gui

release: fdt fdt-gw fdt-intranet fdt-atm fdt-atm-gui fdt-portal-gui
	mkdir -p dist
	cp bin/fdt bin/fdt-gw bin/fdt-intranet bin/fdt-atm bin/fdt-atm-gui bin/fdt-portal-gui dist/
	cd dist && tar czf fdt-gw-linux-amd64.tar.gz fdt-gw
	cd dist && tar czf fdt-intranet-linux-amd64.tar.gz fdt-intranet fdt-portal-gui
	cd dist && tar czf fdt-atm-linux-amd64.tar.gz fdt-atm fdt-atm-gui
	cd dist && tar czf fdt-all-linux-amd64.tar.gz fdt fdt-gw fdt-intranet fdt-atm fdt-atm-gui fdt-portal-gui
	@echo ""
	@echo "分发包（按角色选用）:"
	@echo "  dist/fdt-gw-linux-amd64.tar.gz        ← DMZ 网关（推荐）"
	@echo "  dist/fdt-intranet-linux-amd64.tar.gz  ← 内网 Portal（含 fdt-portal-gui）"
	@echo "  dist/fdt-atm-linux-amd64.tar.gz       ← ATM 终端（含 fdt-atm-gui）"
	@echo "  dist/fdt-all-linux-amd64.tar.gz       ← 全套（含全能 fdt）"
	@ls -lh dist/*.tar.gz dist/fdt-gw dist/fdt-intranet dist/fdt-atm dist/fdt-atm-gui dist/fdt-portal-gui dist/fdt 2>/dev/null | sed 's/^/  /'
	@echo ""
	@echo "安装示例:"
	@echo "  # 网关机：解压后"
	@echo "  sudo ./fdt-gw   # 需先有 /etc/fdt/fdt.env（ROLE=gateway）；或用全能包:"
	@echo "  sudo ./fdt install gateway --bundle gateway.tgz   # 旁路有 fdt-gw 则优先拷贝瘦包"

test:
	CGO_ENABLED=0 go test ./...

clean:
	rm -rf bin dist
