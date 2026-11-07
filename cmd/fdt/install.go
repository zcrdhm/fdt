package main

import (
	"flag"
	"fmt"

	"fdt/internal/install"
)

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	role := ""
	if len(args) > 0 && !isFlag(args[0]) {
		role = args[0]
		args = args[1:]
	}
	bundle := fs.String("bundle", "", "密钥包 .tgz（gateway/atm 从内网 outbox 拷来）")
	tenants := fs.String("tenants", "", "网关多租户：tenants.json（每客户 PK_内网/SK_网关/飞书凭据）")
	server := fs.String("server", "", "ATM: 内网 Portal 根地址，如 http://10.0.0.5:8080")
	device := fs.String("device", "atm-01", "设备 id")
	tenant := fs.String("tenant", "default", "租户 id（内网写入 QR 帧头；多租户时必填客户 id）")
	listen := fs.String("listen", "0.0.0.0:8080", "内网统一监听地址")
	prefix := fs.String("prefix", "/opt/fdt", "二进制安装前缀")
	mock := fs.Bool("mock", false, "网关：启用 mock 飞书（仅联调；多租户时强制各租户 mock）")
	domain := fs.String("domain", "", "网关：公网域名（启用 Let's Encrypt 自动证书）")
	email := fs.String("email", "", "网关：ACME 通知邮箱")
	noSD := fs.Bool("no-systemd", false, "不写 systemd、不自动启动")
	force := fs.Bool("force", false, "覆盖已有密钥")
	_ = fs.Parse(args)
	if role == "" {
		return fmt.Errorf("用法: fdt install intranet|gateway|atm [选项]")
	}
	return install.Run(install.Options{
		Role: role, Bundle: *bundle, Tenants: *tenants, Gateway: *server, DeviceID: *device,
		Tenant: *tenant, Listen: *listen, Prefix: *prefix, Mock: *mock,
		Domain: *domain, Email: *email,
		NoSystemd: *noSD, Force: *force,
	})
}

func isFlag(s string) bool { return len(s) > 0 && s[0] == '-' }
