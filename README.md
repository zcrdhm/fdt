# 离线数据摆渡系统（FDT）

气隙内网 ↔ 飞书审批 ↔ ATM 式摆渡终端的**完全离线**数据导入/导出系统。文件数据永不出内网，跨区只传**带签名的单帧二维码令牌**。

设计依据见 [docs/技术方案.md](docs/技术方案.md)。一键部署见 [docs/一键部署.md](docs/一键部署.md)。

## 一键部署（推荐）

交付物只有 **一个二进制 `fdt`**。每台机器一条命令：

```bash
make release                          # → dist/fdt
sudo ./fdt install intranet           # 内网：密钥 + systemd + 打出 U 盘包
sudo ./fdt install gateway --bundle gateway.tgz --mock
sudo ./fdt install atm --bundle atm-01.tgz --server http://<内网IP>:8080
```

内网安装后，U 盘只带走两个小包：`/var/lib/fdt/outbox/gateway.tgz`、`atm-01.tgz`（各角色只需自己的密钥）。配置只有 `/etc/fdt/fdt.env` 一个文件；服务一个端口（`/` UI，`/data/` 取数，`/import/` 导入）。

## 架构（三个信任域）

```
内网区(零出口)  Portal上传 → 清单/Merkle/签QR#1 → 暂存 → 审计/状态服务
                                   │ QR#1(单帧, 人肉带出)
用户手机(互联网) 飞书网页应用(/app) scanCode → 网关后端
                                   │ 飞书OpenAPI + 回调验签
DMZ审批网关     验QR#1(PK_内网) → 建飞书审批 → 双控HSM签QR#2(SK_网关)
                                   │ QR#2(单帧, 人肉带回)
内网ATM终端     验QR#2(PK_网关) → 拉数据 → 写U盘+回读校验 → CAS核销
```

## 模块映射

| 目录 | 职责 |
|---|---|
| `internal/codec` | base45 + 单帧封装（Magic‖base45(deflate(payload))） |
| `internal/crypto` | Ed25519 密钥生成/PEM 读写/签验 |
| `internal/merkle` | SHA-256 Merkle 树、根、包含证明 |
| `internal/model` | 跨模块数据结构（QR#1/QR#2 令牌、清单、数据集） |
| `internal/token` | 令牌签名信封、单帧编解码、验签、有效期校验 |
| `internal/manifest` | 遍历/流式哈希/类型探测、压缩包安全展开、DLP、full/summary 单帧分级 |
| `internal/store` | SQLite 存储：申请单、令牌生命周期（租约+CAS核销）、审计哈希链、设备、USB操作 |
| `internal/hsm` | 签名器抽象：软件签名器、双控签名器、PKCS#11 桩 |
| `internal/identity` | 申请人身份源：AD/LDAP(search-then-bind) + Mock，Portal 认证用 |
| `internal/feishu` | 飞书审批边界：接口 + Mock + Live OpenAPI 客户端 + 回调解密验签 |
| `internal/portal` | 内网上传服务（业务逻辑 + HTTP） |
| `internal/datasvc` | 数据服务：设备鉴权 + tar 流式下发 + 目录 Merkle 重算 |
| `internal/gateway` | DMZ 网关：**多租户**验 QR#1→建审批→签 QR#2 + 回调（按帧头 tenant 路由、每租户独立密钥、隔离） |
| `internal/atm` | 摆渡终端：状态机、USB/扫码枪抽象、写盘校验回滚、核销；导出+导入 |
| `internal/importer` | 导入方向：AV/DLP 扫描器 + 建单签QR#1 + 收数据重算Merkle防换盘 + 释放/拦截 + HTTP |
| `internal/e2e` | 端到端编排（demo 与测试共用） |
| `cmd/fdt-*` | keygen / demo / portal / gateway / atm 可执行程序 |
| `webapp/` | 飞书网页应用 H5（扫码→提交→轮询→展示 QR#2；网关 `/app/` 内嵌托管） |
| `webadmin/` | 网关运营后台 + 租户自助开户（`/admin/`、`/onboard/`） |
| `internal/gwadmin` | 租户目录、邀请链接、会话鉴权、热加载 |
| `miniapp/` | （已弃用）旧小程序骨架，仅作参考 |
| `internal/webui` | Portal 前端（go:embed 内嵌，随二进制分发，无 CWD 依赖） |

## 真实 vs Mock 边界

**真实实现**：Ed25519 签验、SHA-256、Merkle 树与绑定、base45/deflate 单帧编解码、清单遍历与压缩包安全展开、SQLite 存储、租约+一次性核销 CAS、审计哈希链、飞书回调 AES-256-CBC 解密与 token 校验、ATM 写盘+回读校验+回滚、设备鉴权。

**Mock/桩（生产替换）**：
- `feishu.MockClient` — 替换为 `feishu.LiveClient`（需飞书自建应用凭据）。
- `atm.MockManager`/`MockDevice`/`MockScanner` — 替换为 udev USB 检测 + 串口模式扫码枪。
- `hsm.PKCS11Signer` — 接入真实 HSM（当前返回 `ErrPKCS11Unavailable`）。
- `identity.MockProvider` — 替换为 `identity.LDAPProvider`（`-identity ldap` 接 AD/LDAPS）。
- `manifest.RuleBasedDLP` 的 AV 字段是桩 — 接入真实 DLP/杀毒引擎。
- `importer.RuleBasedScanner` — 开发用（标记 EICAR/可执行扩展名）；生产接真实 AV/DLP 引擎。
- Portal 上传：开发版接收服务端目录路径，生产改为 multipart 上传落隔离区。

## 构建与运行

```bash
# 1. 端到端演示（离线，无需飞书/硬件；自动建样例数据，跑通导出+导入两条链路）
go run ./cmd/fdt-demo

# 2. 生成信任链密钥
go run ./cmd/fdt-keygen -out keys

# 3. 分别起服务（三个终端）
go run ./cmd/fdt-portal  -db fdt.db -internal-key keys/sk_internal.pem -staging staging \
    -identity mock -register-device atm-01 -device-secret dev-secret
go run ./cmd/fdt-gateway -mock -internal-pub keys/pk_internal.pem -gateway-key keys/sk_gateway.pem
# -identity mock 启用样例 AD 账号（zhangsan/lisi，密码 pass123）；生产用 -identity ldap
# 浏览器访问 http://127.0.0.1:8080/（Portal 自带前端，同源无跨域），建单并显示 QR#1

# 4. ATM 兑付（把网页应用/网关得到的 QR#2 帧喂给它）
go run ./cmd/fdt-atm -db fdt.db -gateway-pub keys/pk_gateway.pem \
    -datasvc-url http://127.0.0.1:8081 -device-id atm-01 -device-secret dev-secret \
    -usb-dir /mnt/usb -qr2 "FDT1....."

# 4b. ATM 导入（U盘→内网）：先 start 出 QR#1，审批后再 complete
go run ./cmd/fdt-atm -mode import -import-phase start -db fdt.db -gateway-pub keys/pk_gateway.pem \
    -importer-url http://127.0.0.1:8082 -device-id atm-01 -device-secret dev-secret \
    -usb-dir /mnt/usb -qr-out qr1.png -applicant-id e1 -applicant-name 张三 -reason 导入 -cls internal
go run ./cmd/fdt-atm -mode import -import-phase complete -db fdt.db -gateway-pub keys/pk_gateway.pem \
    -importer-url http://127.0.0.1:8082 -device-id atm-01 -device-secret dev-secret \
    -usb-dir /mnt/usb -req-id <REQID> -qr2 "FDT1....."

# 测试
go test ./...
```

### 多租户 / SaaS（共享网关服务多客户）

一套共享网关 + 一个飞书网页应用可服务多个客户；客户只需在自己内网跑 Portal/datasvc/ATM（气隙决定这半套必须 on-prem）。

```bash
# 每客户内网
sudo ./fdt install intranet --tenant acme
# 共享网关一键多租户（见 config/tenants.example.json）
sudo ./fdt install gateway --tenants ./tenants.json
# 或高级：go run ./cmd/fdt-gateway -tenants /etc/fdt/tenants.json
# 高敏客户：单租户 install gateway --bundle gateway.tgz（租户 default）
```

QR 帧头带明文 tenant（`FDT1.<tenant>.…`），网关据此选对应租户密钥验签；令牌签名内也含 tenant 并交叉校验，防串路由。每客户 ATM 只信任自己那把 `PK_网关`。详见 [docs/技术方案.md](docs/技术方案.md) §13 与 [docs/部署说明.md](docs/部署说明.md) §4.10。

## 关键安全不变量

- **落盘才核销**：QR#2 只有在数据写入 U 盘并回读重算 Merkle 根一致后，才通过原子 CAS 核销；任何失败（无盘/容量/写保护/IO/拔盘/校验不符）都不消耗令牌，可重试。
- **哈希绑定**：审批绑定的是 Merkle 根；ATM 只放行能重算出同一根的数据，内容被换即拦截。
- **无盘拒扫**：先插 U 盘后扫码；无盘时扫码直接拒绝并提示"未检测到USB存储设备"。
- **扫码即数据**：扫码枪输入仅作数据、必须匹配 `FDT1.` 前缀并验签，绝不入 shell（防 HID/QR 注入）。
- **审计不可篡改**：审计事件构成 SHA-256 哈希链，`VerifyAuditChain` 可检出篡改。
- **回调验签**：飞书回调经 AES-256-CBC 解密 + verification token 校验后才触发签名。

## 生产化开关（已实现）

| 能力 | 怎么开 |
|---|---|
| PKCS#11 签 QR#2 | `fdt-gateway -pkcs11-module … -pkcs11-label … -gateway-pub …`；双控 `-dual-control 2` + `POST /hsm/approve` |
| 网关会话持久化 | `-session-db /var/lib/fdtgw/sessions.db`（重启不丢未取走的 QR#2） |
| 回调来源限制 | `-callback-allow /etc/fdtgw/callback.cidrs` |
| 飞书表单 widget | `-feishu-form config/feishu-form.example.json`（id 换成飞书后台真实控件 id） |
| Postgres | Portal/ATM `-db-driver postgres -db 'postgres://…'` |
| ClamAV | Portal `-clamd unix:///var/run/clamav/clamd.ctl`（导入 fail-closed） |
| multipart 上传 | Portal 前端选文件，或 `POST /requests` multipart；勿再依赖裸 `sourceDir` |
| ATM 真硬件 | `-usb sysfs -scanner /dev/ttyACM0` 或 `-camera /dev/video0`；可选 `-kiosk-usb-ports 1-1` |
| 设备 mTLS | `fdt-keygen -device-ca -device atm-01 -server datasvc`；Portal `-tls-cert/-tls-key/-tls-client-ca`；ATM `-client-cert/-client-key/-ca` |

令牌线格式为 **canonical CBOR → Ed25519 → deflate → base45**（`FDT1.<tenant>.…`）。导出可选加密与水印仍待业务定策。
