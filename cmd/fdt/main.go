// Command fdt is the single binary for build, install and run.
//
//	fdt install intranet|gateway|atm   # one-click role setup
//	fdt run                            # start from /etc/fdt/fdt.env
//	fdt demo|keygen|portal|gateway|atm # advanced / legacy entry points
package main

import (
	"fmt"
	"os"
)

const usage = `FDT — 离线数据摆渡（单二进制）

一键部署（推荐）：
  sudo ./fdt install intranet [--tenant acme]   # 内网：密钥 + 服务 + U 盘密钥包
  sudo ./fdt install gateway --bundle gateway.tgz --domain gw.example.com --email ops@x.com
  sudo ./fdt install gateway --tenants tenants.json [--mock]        # 预置多租户
  sudo ./fdt install atm --bundle atm-01.tgz --server http://10.0.0.5:8080
  # /admin：绑定域名 → Let's Encrypt → 租户注册链接；客户 /console 上传 PEM、绑飞书

日常：
  sudo systemctl status fdt                # 内网/网关守护进程
  fdt run                                  # 前台按 /etc/fdt/fdt.env 启动
  fdt demo                                 # 离线端到端演示

高级（兼容旧命令）：
  fdt portal|gateway|atm|keygen [flags…]
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "install":
		err = cmdInstall(args)
	case "run":
		err = cmdRun(args)
	case "demo":
		err = cmdDemo(args)
	case "keygen":
		err = cmdKeygen(args)
	case "portal":
		err = cmdPortal(args)
	case "gateway":
		err = cmdGateway(args)
	case "atm":
		err = cmdATM(args)
	case "version":
		fmt.Println("fdt 1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
