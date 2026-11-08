# Windows ATM 编译与部署

本文说明如何在 **Windows** 上跑 FDT ATM（触屏/网页兑付端）。  
当前 Windows 为**可用联调 / 轻量部署**路径；Linux 机柜仍是生产首选（真机 USB 自动挂载、V4L2 摄像头、串口枪）。

## 1. 能力对照

| 能力 | Windows | Linux |
|---|---|---|
| 二进制 / 网页 UI（`:9080`） | ✅ | ✅ |
| 导出兑付 / 导入 | ✅ | ✅ |
| U 盘 | ✅ `USB=windows` 自动识别可移动/USB 盘符 | ✅ `USB=sysfs` 自动挂载 |
| 扫码预览（浏览器摄像头） | ✅ 点「扫码」弹预览窗 | ✅ |
| 本机服务端摄像头（`CAMERA=`） | ❌ 未实现 | ✅ `/dev/video0` + ffmpeg |
| 串口扫码枪 | ❌ 未实现 | ✅ |
| `fdt install atm` 一键装 | ⚠️ 面向 Linux（systemd/脚本）；Windows 建议手工目录 | ✅ |

> 扫码请用 UI 里的 **「扫码」**（浏览器预览）。本机 `CAMERA` 回退在 Windows 上会失败，属预期。

## 2. 编译

需 Go 1.22+（与仓库 `go.mod` 一致）。**务必 `CGO_ENABLED=0`**。

### 2.1 在 Linux / macOS 交叉编译

```bash
# amd64（常见 PC / 多数工控机）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/fdt.exe ./cmd/fdt

# arm64（部分 Windows on ARM）
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/fdt-arm64.exe ./cmd/fdt
```

产物：单个 `fdt.exe`（内嵌 Portal/ATM UI，无需额外前端包）。

### 2.2 在 Windows 本机编译

PowerShell / CMD（已安装 Go）：

```bat
set CGO_ENABLED=0
go build -trimpath -ldflags="-s -w" -o fdt.exe .\cmd\fdt
```

### 2.3 快速自检

```bat
fdt.exe -h
fdt.exe atm -h
```

## 3. 准备密钥包

内网 Portal 安装完成后，U 盘 / outbox 里有 **`atm-01.tgz`**（设备 id 以实际为准）。

在 Windows 上解压到 ATM 密钥目录，例如：

```bat
mkdir C:\fdt-atm\etc\keys
tar -xf atm-01.tgz -C C:\fdt-atm\etc\keys
dir C:\fdt-atm\etc\keys
```

至少需要：

- `pk_gateway.pem` — 验 QR#2  
- 设备相关 pem / secret（若包内带有，与内网 `DEVICE_ID` 一致）

也可用 Linux 装好的 `/etc/fdt-atm/keys/` 整目录拷过来。

## 4. 目录与配置

建议布局：

```text
C:\fdt-atm\
  bin\fdt.exe
  etc\fdt.env
  etc\keys\          ← 解压 atm-*.tgz
  var\               ← 本地库、维护密码文件
```

新建 `C:\fdt-atm\etc\fdt.env`（示例，按现场改）：

```ini
ROLE=atm
DATA_DIR=C:\fdt-atm\var
KEYS_DIR=C:\fdt-atm\etc\keys
DEVICE_ID=atm-01
DEVICE_SECRET=与内网签发一致
DATA_URL=http://10.0.0.5:8081/data
IMPORT_URL=http://10.0.0.5:8081/import

# Windows：认真盘（可移动盘 + USB 接口的固定盘）；不要用 Linux 的 sysfs
USB=windows
# 仅联调假盘时才用：USB=mock + USB_DIR=E:\

ATM_UI_LISTEN=127.0.0.1:9080
ATM_UI_TLS=0
ATM_UI_URL=http://127.0.0.1:9080
# 无 HTTPS：ATM_UI_TLS=0，下面两项留空即可（不会读证书）
ATM_UI_TLS_CERT=
ATM_UI_TLS_KEY=
# HTTP 80：ATM_UI_LISTEN=0.0.0.0:80  ATM_UI_TLS=0  ATM_UI_URL=http://127.0.0.1
# HTTPS 443：ATM_UI_LISTEN=0.0.0.0:443  ATM_UI_TLS=1
#   ATM_UI_TLS_CERT=C:\fdt-atm\etc\keys\server.pem
#   ATM_UI_TLS_KEY=C:\fdt-atm\etc\keys\server.key
#   ATM_UI_URL=https://127.0.0.1
# 首次引导明文；启动后哈希写入 ATM_MAINT_PASSWORD_FILE，请删掉本行明文
ATM_MAINT_PASSWORD=fdt-maint
ATM_MAINT_PASSWORD_FILE=C:\fdt-atm\var\atm_maint_password
ATM_IMPORT_FIELDS=C:\fdt-atm\etc\import-fields.json

# Windows 勿依赖；服务端摄像头未实现
CAMERA=
SCANNER=
```

把仓库里的 `config/atm-import-fields.example.json` 拷成 `C:\fdt-atm\etc\import-fields.json`，按现场改「导入申请」字段（见 [一键部署.md](一键部署.md)「ATM 如何改导入申请表单字段」）。改完重启 `-ui` 进程。

说明：

- **`USB=windows`**：自动枚举 D:–Z: 中可移动盘，以及总线类型为 USB 的盘；插上即识别，无需写盘符。联调假盘才用 `USB=mock` + `USB_DIR=`。  
- **`DATA_URL` / `IMPORT_URL`**：必须指向内网 **`:8081`** 的 `/data`、`/import`（不是 Portal 网页端口，也不是 ATM UI 端口）。  
- **`ATM_UI_LISTEN` / `ATM_UI_TLS`**：仅控制本机触屏网页；可改为 80/443 + 证书。  
- **`DEVICE_SECRET`**：与内网设备鉴权一致；不对则拉数/导入会 401。  
- **`ATM_IMPORT_FIELDS`**：本机导入表单 JSON；与 Portal 无关。  
- 路径可用正斜杠 `C:/fdt-atm/...`，Go 均可识别。

## 5. 启动

### 5.1 独立 GUI（推荐，无需管理员）

仓库独立目录 `atm-gui/` → **`fdt-atm-gui.exe`**：

- **普通用户**双击即可（不写 `C:\Program Files` / 不需 Administrator）
- **首次启动**弹出配置向导（Portal `:8081`、设备密钥、`pk_gateway.pem`、维护密码）
- 配置写入 `%AppData%\FDT-ATM\fdt.env`（可用 `FDT_ATM_HOME` 覆盖）
- 以后启动直接进 ATM；`-setup` 可重新配置
- 维护密码在界面「维护模式」修改（文件内为 bcrypt）

```bat
:: 构建机
make windows-atm-gui
:: 把 dist\windows-amd64\fdt-atm-gui.exe 拷到任意用户可写目录后双击

fdt-atm-gui.exe
fdt-atm-gui.exe -setup
```

需 Edge WebView2 Runtime（Win10/11 一般自带）。

### 5.2 网页 / 触屏 UI（浏览器）

```bat
cd /d C:\fdt-atm
bin\fdt.exe atm -env etc\fdt.env -ui
```

浏览器打开：http://127.0.0.1:9080/

- 插入 U 盘（确认盘符与 `USB_DIR` 一致）  
- 导出：扫 QR#2 或粘贴令牌 → 兑付写盘  
- 导入：选 U 盘文件 → 生成 QR#1 → 审批后扫 QR#2 完成  

### 5.3 命令行兑付（无 UI）

```bat
bin\fdt.exe atm -env etc\fdt.env -qr2 "FDT1...."
```

### 5.4 可选：Chrome 伪 kiosk

Windows 无 `fdt-atm-kiosk` 脚本，可自行：

```bat
start "" "C:\Program Files\Google\Chrome\Application\chrome.exe" ^
  --kiosk --app="http://127.0.0.1:9080/?kiosk=1&run=1" ^
  --user-data-dir="%LOCALAPPDATA%\fdt-atm-chrome-kiosk" ^
  --no-first-run --noerrdialogs
```

先保证 `-ui` 进程已在跑。

### 5.5 开机自启（可选）

推荐直接启动 `bin\fdt-atm-gui.exe`（自带服务+窗口）。旧方式：

任选其一：

- 任务计划程序：登录时运行 `fdt.exe atm -env ... -ui`  
- NSSM / WinSW 注册为服务（服务账号需能访问 U 盘盘符；无交互桌面时触屏需另配自动登录 + 开浏览器）

## 6. 网络与权限

- ATM ↔ 内网：只放行 **8081**（`/data` `/import`）；**不要**对 ATM 网段开放 Portal UI 端口（默认 8080）。  
- 本机防火墙允许 `ATM_UI_LISTEN`（仅本机可改为 `127.0.0.1:9080`）。  
- 杀软勿隔离 `fdt.exe` 与 U 盘写入路径。  
- 浏览器扫码需允许摄像头权限（预览窗）。

## 7. 常见问题

| 现象 | 处理 |
|---|---|
| 提示未插 U 盘 | 确认盘符；`USB=mock` 且 `USB_DIR` 指向实际根目录（如 `E:\`） |
| 扫码后「未配置摄像头 / only supported on Linux」 | 属服务端回退；改用预览窗扫码，或粘贴令牌；清空 `CAMERA=` |
| 兑付 401 / 鉴权失败 | 核对 `DEVICE_ID`、`DEVICE_SECRET`、内网是否登记该设备 |
| 验签失败 | 确认使用本租户 `pk_gateway.pem`，与签发网关一致 |
| `install atm` 在 Windows 异常 | 请按本文手工目录部署；一键安装面向 Linux |

## 8. 与 Linux ATM 的选择建议

- **机柜 / 生产**：Linux（sysfs 挂盘、本机摄像头、kiosk 脚本）。  
- **Windows 工控屏 / 临时窗口机**：按本文部署；U 盘固定盘符 + 浏览器扫码即可完成主流程。

后续若补齐 Windows 真盘枚举或 DirectShow/MediaFoundation 摄像头，再更新本节对照表。
