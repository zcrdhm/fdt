# 一键产物：只有一个二进制
.PHONY: fdt release test clean

VERSION ?= 1.0
GOFLAGS ?= -trimpath -ldflags="-s -w"

fdt:
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/fdt ./cmd/fdt

release: fdt
	mkdir -p dist
	cp bin/fdt dist/fdt
	cd dist && tar czf fdt-linux-amd64.tar.gz fdt
	@echo ""
	@echo "分发包只有 1 个文件: dist/fdt-linux-amd64.tar.gz"
	@echo "解压后:"
	@echo "  sudo ./fdt install intranet"
	@echo "  sudo ./fdt install gateway --bundle /path/gateway.tgz --mock"
	@echo "  sudo ./fdt install atm --bundle /path/atm-01.tgz --server http://内网IP:8080"

test:
	CGO_ENABLED=0 go test ./...

clean:
	rm -rf bin dist
