# FDT ATM GUI（独立目录）

面向**普通用户**（无需 root / Administrator）：

1. **首次启动** → 配置向导（Portal、设备密钥、`pk_gateway.pem`、维护密码、U 盘模式、可选浏览 `import-fields.json`；不选则用内置默认）  
2. 写入当前用户目录下的 `fdt.env` + `import-fields.json`  
3. **以后启动** → 检测到配置齐全则直接开服务 + 窗口  
4. 维护密码 / 导入字段可在 ATM「维护模式」中修改

## 用户配置目录（自动创建）

| 平台 | 默认路径 |
|---|---|
| Linux / macOS | `~/.config/FDT-ATM/` |
| Windows | `%AppData%\FDT-ATM\`（`os.UserConfigDir`） |

可用环境变量 **`FDT_ATM_HOME`** 覆盖。

目录内容示例：

```text
FDT-ATM/
  fdt.env
  keys/pk_gateway.pem
  var/fdt.db
  var/atm_maint_password   # bcrypt 哈希
  import-fields.json
  gui-profile/             # 窗口配置缓存
```

## 构建

```bash
# 在仓库根目录
make fdt-atm-gui
make windows-atm-gui
```

## 运行

```bash
./bin/fdt-atm-gui              # 首次自动进向导
./bin/fdt-atm-gui -setup       # 强制重新配置
./bin/fdt-atm-gui -env /path/to/fdt.env
```

## 窗口与防退出

| 平台 | 实现 |
|---|---|
| Windows | WebView2 + 低级键盘钩子（挡 Win / Alt+F4 / Alt+Tab / Esc 等）+ 去掉关闭按钮 + 关窗自动重开 |
| Linux / macOS | Chrome/Chromium/Edge `--kiosk` + 尽量关闭 GNOME Super 键 + 关窗自动重开 |

真正退出：维护模式 → **退出程序** → 输入维护密码。首次配置向导窗口可正常关闭。

Linux 非 root 默认 U 盘模式为 `mock`（用户目录下模拟盘）；有 root 或已能挂载时可在向导里选 `sysfs`。Windows 选 `windows` 识别真 U 盘，一般不需要管理员。
