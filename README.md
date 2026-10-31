# 长城 Clash

面向 GT-AX6000、带 ASUS GO / Koolshare 软件中心的 ASUSWRT-Merlin 独立插件。以 mihomo 为代理内核，提供中文配置、订阅、代理组、规则、连接、DNS、设备分流、内核更新回退和备份管理。

## 当前交付：0.3.6 USB 完整版

0.3.6 修复批量测速失败被忽略、停机残留生效规则、保存备用配置重启当前代理、订阅失败频繁重试四项问题。批量测速显示成功/失败数量，单项可重试；备用配置和内容未变的当前配置保存不重启内核。订阅失败按 5/10/20/40/80/120 分钟退避，状态持久化，配置页显示最近尝试、失败原因及下次更新/重试时间。

停机节点页增加本地缓存、文件集合和内嵌集合预览（不下载、不模拟最终组筛选）；DNS 页增加清空 DNS/Fake-IP 缓存入口。在线集合缓存改为按来源配置区分，同名不同源不会共用缓存；旧版本缓存不直接复用，首次启动需要重新加载对应集合，原文件保留。

0.3.5 将「代理」页更名为「节点」。停机时读取当前已保存配置及 YAML 覆写，显示静态节点和代理组，不启动内核、不下载订阅。动态集合、筛选及自动选择结果仍需内核加载；预览不显示生效线路或旧延迟。启动后使用 mihomo 实时数据并开放测速、选择线路。节点页提供「域名 / IP 分流」和「按设备分流」入口，分别进入规则和设备页。

0.3.4 修复 TPROXY 预检仅查看用户态帮助的缺口：先在未挂接流量的临时 mangle 链实际创建目标规则，失败后尝试固件 modprobe 的 xt_TPROXY / xt_TPROXY.ko 两种名称并重测。IPv4 失败时不启动接管；IPv6 自动模式不支持时保留防绕过，强制模式则报错。只清理本次成功创建的临时链，状态轮询不创建检测链。不下载、强制加载或卸载第三方模块。参考 [fancyss 模块加载](https://github.com/hq450/fancyss/blob/3.0/fancyss/ss/ssconfig.sh#L5007)。环境诊断新增内存总量/可用量、模块和内核目标列表；没有实机数据时不推断 OOM。

Go 全包测试、vet 和模块加载/临时链清理模拟通过；仍待 GT-AX6000 上启用代理验证。

0.3.3 修复旧固件上插件样式无法加载：CSS 使用带版本的文件名，不再使用查询参数。3004.388.4 上游 httpd 的静态 do_file 会直接打开请求路径，而旧版带 ?v= 的路径无法匹配磁盘文件。参见 [固件源码](https://github.com/RMerl/asuswrt-merlin.ng/blob/3004.388.4/release/src/router/httpd/httpd.c#L832)。隔离测试模拟此行为复现旧页面样式丢失，并验证新版从真实安装包加载样式。用户已确认0.3.2安装成功，截图显示管理服务已连接；透明代理仍未验收。

0.3.2 修复 0.3.1 版本文件 CRLF 导致安装器误报“管理程序无法在当前固件运行”。构建统一写入 LF，审计严格检查原始字节；安装日志分别报告启动失败和版本不匹配。0.3.1 此处失败发生在修改现有安装之前，直接使用新版完整包重试即可。

[下载 USB 安装包](https://github.com/whossyourrddady/cwclash/releases/download/v0.3.6/cwclash_hnd_v8_usb_0.3.6.tar.gz) · [版本说明、源码与校验文件](https://github.com/whossyourrddady/cwclash/releases/tag/v0.3.6)。这是私人仓库，需要使用有权限的 GitHub 账号访问。

使用官方 USB2JFFS 将健康的 EXT4 U 盘挂载到 `/jffs`，再通过软件中心离线安装 **`cwclash_hnd_v8_usb_0.3.6.tar.gz`**。一份包包含管理端、界面和官方 [mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.33) Linux ARM64 内核，无需先装引导包。

- 包大小约 25.4 MB，持久文件约 58.5 MiB；安装器额外保留至少 128 MiB，供更新和回退使用。规则、GeoIP、配置和缓存还会继续占用 U 盘空间。
- 安装前核对 `/jffs`、软件中心和插件目标目录确实位于同一块可写 USB EXT4 分区；条件不符时拒绝安装。
- 内核原始文件保存在 U 盘，启动时按块校验并复制到内存执行，省去 XZ 解压和整文件内存读取；升级保留当前版本和一个上一版本。
- 配置、订阅、手动线路选择、规则与 GeoIP 缓存保存在 U 盘；运行程序、PID、控制令牌和网络所有权记录放在 `/tmp/cwclash-run`。
- 挂载变化时停止代理接管；另有依靠内存程序和挂载记录工作的独立清理任务。异常硬件造成的 I/O 卡顿和真实拔盘恢复仍需实机验证。

适配目标为 GT-AX6000 / ARM64、固件 3004.388.4、软件中心 1.9.57。0.2.0 实机日志显示 EXT4 挂载与目标目录检查通过，但在 USB 设备识别处停止；**0.2.1 修复此项兼容检查，尚待实机重试验收**。本地已完成安装模拟、实际管理接口往返、挂载校验和 Windows 原生 mihomo 转发/缓存检查；这些结果不能代替路由器上的透明代理、开关机与 IPv6 验证。操作步骤见 [安装说明](docs/INSTALL.md)。

0.2.1 支持 USB 硬件祖先直接出现在块设备路径中的布局，也支持 `/sys/block` 下通过整盘 `device` 链接访问硬件的旧布局；缺少 `/sys/dev/block` 索引时，改查 class/block 或 block 入口并核验相同的设备编号。相关内核接口见 [sysfs-dev](https://www.kernel.org/doc/Documentation/ABI/testing/sysfs-dev)。

## 0.3.5 日常操作优化

概览分开展示管理服务、内核、DNS 模式与存储检查入口。DNS 模式不是连通性检测结果；存储显示最近一次手动检查结果。

订阅显示已用/剩余流量及到期时间，缺失字段明确标注。节点支持名称/延迟排序、隐藏测速超时、按配置和代理组收藏；未测速节点不会被当作不可用，收藏仅保存在当前浏览器。设置分为常用、高级、内核存储、备份诊断。网络/规则和设备草稿在切页及其他操作刷新后保留，保存成功后更新基线，离开页面提示未保存修改。设备页显示当前活动连接的下载量与策略链，不是历史流量统计。状态颜色独立于主题强调色。

## 使用

USB2JFFS 挂载成功后安装 USB 完整包。软件中心打开“长城 Clash”，导入 Clash/mihomo YAML 文件或订阅，启用后选择代理组线路。启用前关闭其他透明代理插件。不自动转换 fancyss 节点数据库或 Xray JSON。

0.3.5 将页面直接放入固件的顶部、左侧菜单和内容框中，使用横向功能标签和紧凑表单，跟随 ASUSWRT / ROG / TUF / TS 皮肤。页面和数据接口均使用路由器同源地址，不再使用独立应用侧栏或另开窗口。更新后用 Ctrl+F5 刷新旧页面。

**尚未与 FlClash 完全对齐。** 订阅、代理组、测速、规则、连接、DNS、日志和本地备份已有实现；JavaScript 覆写、WebDAV、TUN 和独立的可视化节点/代理组编辑器尚未实现。平台差异、已实现边界及待办见 [FlClash 功能对照](docs/FLCLASH-PARITY.md)。

本轮通过构建、包审计和隔离界面验证，包括九页导航、表单、皮肤和样式隔离。测试使用真实插件 ASP 和前端资源，但外围固件及数据为替身；实际固件菜单、安装、代理和重启仍待实机验收。

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
