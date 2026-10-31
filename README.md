# 长城 Clash

面向 GT-AX6000、带 ASUS GO / Koolshare 软件中心的 ASUSWRT-Merlin 独立插件。以 mihomo 为代理内核，提供中文配置、订阅、代理组、规则、连接、DNS、设备分流、内核更新回退和备份管理。

## 当前交付：0.2.1 USB 完整版

[下载 USB 安装包](https://github.com/whossyourrddady/cwclash/releases/download/v0.2.1/cwclash_hnd_v8_usb_0.2.1.tar.gz) · [版本说明、源码与校验文件](https://github.com/whossyourrddady/cwclash/releases/tag/v0.2.1)。这是私人仓库，需要使用有权限的 GitHub 账号访问。

使用官方 USB2JFFS 将健康的 EXT4 U 盘挂载到 `/jffs`，再通过软件中心离线安装 **`cwclash_hnd_v8_usb_0.2.1.tar.gz`**。一份包包含管理端、界面和官方 [mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.33) Linux ARM64 内核，无需先装引导包。

- 包大小约 25.40 MB，持久文件约 58.5 MiB；安装器额外保留至少 128 MiB，供更新和回退使用。规则、GeoIP、配置和缓存还会继续占用 U 盘空间。
- 安装前核对 `/jffs`、软件中心和插件目标目录确实位于同一块可写 USB EXT4 分区；条件不符时拒绝安装。
- 内核原始文件保存在 U 盘，启动时按块校验并复制到内存执行，省去 XZ 解压和整文件内存读取；升级保留当前版本和一个上一版本。
- 配置、订阅、手动线路选择、规则与 GeoIP 缓存保存在 U 盘；运行程序、PID、控制令牌和网络所有权记录放在 `/tmp/cwclash-run`。
- 挂载变化时停止代理接管；另有依靠内存程序和挂载记录工作的独立清理任务。异常硬件造成的 I/O 卡顿和真实拔盘恢复仍需实机验证。

适配目标为 GT-AX6000 / ARM64、固件 3004.388.4、软件中心 1.9.57。0.2.0 实机日志显示 EXT4 挂载与目标目录检查通过，但在 USB 设备识别处停止；**0.2.1 修复此项兼容检查，尚待实机重试验收**。本地已完成安装模拟、实际管理接口往返、挂载校验和 Windows 原生 mihomo 转发/缓存检查；这些结果不能代替路由器上的透明代理、开关机与 IPv6 验证。操作步骤见 [安装说明](docs/INSTALL.md)。

0.2.1 支持 USB 硬件祖先直接出现在块设备路径中的布局，也支持 `/sys/block` 下通过整盘 `device` 链接访问硬件的旧布局；缺少 `/sys/dev/block` 索引时，改查 class/block 或 block 入口并核验相同的设备编号。相关内核接口见 [sysfs-dev](https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-dev)。

## 使用

USB2JFFS 挂载成功后安装 USB 完整包。打开“长城 Clash”，导入 Clash/mihomo YAML 文件或订阅，选择代理组后启用。启用前关闭其他透明代理插件。不自动转换 fancyss 节点数据库或 Xray JSON。

TCP 使用 REDIRECT，UDP 使用 TPROXY；设备规则按 IP 生效，双栈设备可分别设置 IPv4 和 IPv6 地址。MAC 仅用于识别展示。失败时清理本插件的代理与 DNS 接管；默认 Redir-Host，Fake-IP 客户端缓存可能需等待过期。

## 开发与构建

需要 Node.js 22.12+、Go 1.23+。此工作区使用经官方 SHA-256 校验的便携 Go 1.27.3，位于忽略的 `.tools/go`，不修改系统 PATH。先按 `dependencies/mihomo.lock.json` 将官方 Linux ARM64 `.gz` 放到 `.tools/mihomo`；构建脚本校验大小、摘要和 AArch64 ELF 类型。

```powershell
cd web
npm ci
cd ..
node scripts/build.mjs --usb
node scripts/audit-package.mjs --usb
# 准备 dependencies/upstream-sources.lock.json 对应材料后生成源码交付
node scripts/build-sources.mjs
```

默认查找 `.tools/go/bin/go.exe`（Linux 为 `.tools/go/bin/go`），也可设置 `CWCLASH_GO`。管理程序采用 `CGO_ENABLED=0 GOOS=linux GOARCH=arm64` 构建，压缩为单文件 tar.gz，通过固件已有 tar 解包；不依赖 `command -v`、独立 gzip、mktemp 或 sha256sum，SHA-256 可使用固件 OpenSSL。包内提供逐文件摘要、许可证和版本化静态资源。

不加 `--usb` 的构建仍保留内置 JFFS / XZ 方案，`--bootstrap` 仍可构建管理端引导包；它们不是本次 USB 交付的安装入口。USB 版不能与 `--bootstrap` 同时构建。

```powershell
$env:GOCACHE=Join-Path (Get-Location) '.cache/go-build'
$env:GOMODCACHE=Join-Path (Get-Location) '.tools/gomod'
$env:GOTMPDIR=Join-Path (Get-Location) '.cache/tmp'
$env:TMP=$env:GOTMPDIR
$env:TEMP=$env:GOTMPDIR
& ./.tools/go/bin/go.exe test ./... -count=1
& ./.tools/go/bin/go.exe vet ./...
```

设置 `CWCLASH_TEST_CORE_PATH` 为 Windows 原生 mihomo 路径，可运行 `TestNativeProtocolLoopback`，包括 USB 布局中原始内核及持久 HTTP provider 缓存检查。开发模式只使用回环服务，不调用路由器防火墙和策略路由。开发服务器仅监听 `127.0.0.1`。

## 接口与数据

软件中心通过 `cwclash_api.sh` 认证桥调用固定操作白名单。请求编号限制为 1–8 位，编码参数最多 1600 字符，文件使用 768 字节块；大配置、设置和设备保存请求通过内存分块传送。管理服务和 mihomo 控制接口均只监听回环并要求独立随机密钥。

持久目录为 `/koolshare/cwclash`，USB 缓存位于其 `data` 子目录。软件中心卸载会移除服务和页面，保留配置及内核；彻底删除前先导出备份。私有订阅、固件备份、日志和本地运行状态不进入源码包。

GPL-3.0-or-later。组件来源与许可证见 `THIRD_PARTY.md`、`LICENSE`，内核对应源码见 [源码说明](docs/CORE-SOURCE.md)。
