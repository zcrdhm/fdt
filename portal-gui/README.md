# FDT Portal GUI（独立目录）

面向**普通用户**的内网 Portal 一键程序（UI + ATM 数据口）：

1. **首次启动** → 图形配置向导（或 `-setup-cli`）  
2. 写入用户目录 `fdt.env`、密钥、表单 JSON  
3. **安装为后台服务**（Linux: `systemd --user`；Windows: 登录计划任务）并打开浏览器  
4. 以后双击 / 再运行：确保服务在跑并打开管理页（`-foreground` 可改回前台）  

```bash
./fdt-portal-gui                     # 配置→装服务→开浏览器
./fdt-portal-gui -install-service    # 仅重装/启动服务
./fdt-portal-gui -uninstall-service  # 卸载服务
./fdt-portal-gui -foreground         # 不装服务，当前终端前台跑
```


## 用户配置目录

| 平台 | 默认路径 |
|---|---|
| Linux / macOS | `~/.config/FDT-Portal/` |
| Windows | `%AppData%\FDT-Portal\` |

覆盖：环境变量 **`FDT_PORTAL_HOME`**。

```text
FDT-Portal/
  fdt.env
  keys/                 # sk/pk_internal、sk/pk_gateway、可选 server.pem/key
  var/                  # fdt.db、staging、quarantine、released、uploads
  form-fields.json      # Portal 导出申请字段
  import-fields.json    # ATM 导入字段模板（可拷到 ATM）
  gui-profile/
```

## 构建

```bash
make fdt-portal-gui
make windows-portal-gui
```

## 运行

```bash
./bin/fdt-portal-gui              # 首次进向导
./bin/fdt-portal-gui -setup       # 强制图形重配
./bin/fdt-portal-gui -no-browser  # 只起服务

# 命令行初始化（不启动服务）
./bin/fdt-portal-gui -setup-cli \
  -admin-pass 'change-me' \
  -listen 0.0.0.0:8080 \
  -data-listen 0.0.0.0:8081 \
  -tenant site-a \
  -domain portal.corp.local \
  -export-pk-internal /tmp/pk_internal.pem \
  -form-fields ./form-fields.json \
  -import-fields ./import-fields.json
```

## 可配置项摘要

| 项 | 说明 |
|---|---|
| LISTEN / DATA_LISTEN | Portal UI 端口、ATM `/data` `/import` 端口 |
| DOMAIN / PUBLIC_BASE | 对外展示域名与根 URL |
| TENANT | 多租户站点 ID |
| TLS / 证书 | HTTPS；可粘贴或放入 `keys/server.pem`+`server.key` |
| DATA_DIR / KEYS_DIR | 存储与密钥位置；向导支持系统选目录、读写权限测试 |
| 管理账号 | PORTAL_ADMIN_* |
| IDENTITY | mock / ldap / none |
| 表单 | 导出（Portal）+ 导入模板（ATM）；浏览 JSON 或默认 |
| 密钥 | 自动生成；可粘贴；另存/下载 `pk_internal.pem` |
| DEVICE_* | 可选预注册 ATM |

ATM 连接本机数据口示例：`http://<Portal主机>:8081/data` 与 `…/import`（端口以 `DATA_LISTEN` 为准）。
