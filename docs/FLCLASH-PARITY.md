# FlClash 功能对照（2026-10-31）

**结论：长城 Clash 0.3.9 尚未与 FlClash 完全对齐。** 常用代理管理已有实现；JavaScript 覆写、WebDAV、TUN 等仍有差距。0.3.8 已增加可视化节点/组编辑，具体范围见下文。桌面和 Android 专属能力不能直接搬到路由器。

本轮实际读取 FlClash 源码，固定于 **`7fd417fb9a9a277c6a9339a8ac87f907df7c25f7`**（2026-10-24），不是只看 README。对照源包括 core/delay.go、core/profile_base.go、core/provider.go、core/geo.go、lib/models/profile.dart、lib/models/clash_config.dart、lib/manager/core_manager.dart，以及 providers/actions 下的 profiles、proxies、backup、scripts、geo_resource。

“已实现”表示有相应代码和入口，不等于不同固件、订阅、协议全部验收；本次按公开行为独立实现，没有把 FlClash 客户端或其修改版内核打包进插件。

| 功能 | 长城 Clash 0.3.9 | 实现位置与边界 |
| --- | --- | --- |
| mihomo 内核 | 已实现 | 官方 Linux ARM64 内核，支持检查更新、离线导入和上一版回退；[upgrade.go](../internal/app/upgrade.go)、[core_storage.go](../internal/app/core_storage.go)。 |
| 规则、全局、直连模式 | 已实现 | 概览切换，真实更新内核与本插件接管规则；[core.go](../internal/app/core.go)、[network.go](../internal/app/network.go)。 |
| 代理组选择与延迟测试 | 已实现 | 节点页支持停机配置预览（静态成员、含 YAML 覆写，以及本地/内嵌集合和已加载的在线集合缓存）；最终组成员由内核运行时加载、筛选。运行时支持策略组、节点选择、单个/批量测速、手动选择按配置保留；支持按延迟/名称排序、隐藏测速超时及当前浏览器按配置/组收藏；[App.vue](../web/src/App.vue)、[api.go](../internal/app/api.go)。 |
| 订阅链接、文件导入、编辑 | 已实现 | 多配置切换、YAML 编辑、自动更新、User-Agent、导出；订阅额度与到期时间可读展示；上游未提供的字段显示未提供。 |
| 自定义规则与 YAML 覆写 | 已实现 | 域名/CIDR 分流、自定义 mihomo 规则、YAML 深度合并；路由器控制端口、接管方式等由插件最后统一设置；[config.go](../internal/app/config.go)。 |
| JavaScript 覆写脚本 | 未实现 | YAML 覆写不能执行 FlClash 的 JavaScript 脚本，也不能宣称二者兼容。 |
| 自定义节点和代理组 | 已提供可视化编辑 | 0.3.8 支持新增/复制/改名/删除、凭据和 TLS/传输字段、成员与集合排序、检测/筛选/策略参数及高级 YAML；配置内常见引用自动更新。编辑原始配置，在线订阅默认另存副本，非 FlClash 数据库或其每配置覆写结构；见 [编辑说明](VISUAL-EDITOR.md)。 |
| 在线/本地规则和代理集合 | 已实现 | 集合读取、更新、本地文件上传；USB 版保留 provider 缓存；[config.go](../internal/app/config.go)、[transfers.go](../internal/app/transfers.go)。 |
| 实时连接与流量 | 已实现 | 流量趋势、来源设备、目标、策略链、命中规则、流量和关闭连接。界面显示前300条连接；没有桌面进程详情。 |
| 独立请求分析视图 | 未完整对齐 | 当前以连接表和 DNS 历史提供信息，没有另一个独立请求分析页或 HTTP 内容分析。 |
| DNS 查询与历史 | 已实现，但范围有限 | Fake-IP/Redir-Host、诊断、通过本插件 DNS 转发器的查询记录。不能据此声称监测所有客户端加密 DNS。DNS 页面提供 DNS/Fake-IP 缓存清空入口；[dns.go](../internal/app/dns.go)、[api.go](../internal/app/api.go)。 |
| 日志 | 已实现 | 实时追加、级别过滤、搜索、暂停、导出；内存保留有限数量，非长期集中日志服务。 |
| 本地备份恢复 | 已实现 | 配置、订阅、规则、设备设置和本地资源 ZIP；保留恢复前副本，恢复后代理停止；不含内核和在线缓存。格式为本插件自有格式，不兼容 FlClash 备份包。 |
| WebDAV 备份恢复 | 未实现 | 没有 WebDAV 地址、认证或同步任务。 |
| 系统代理 | 路由器替代方式 | 由 TCP REDIRECT / UDP TPROXY 接管局域网，另有混合代理端口；不修改每台电脑/手机的系统代理开关。 |
| TUN 模式 | 未实现 | 编译配置明确设定 tun.enable=false；现用 REDIRECT/TPROXY，不能把它称为已实现 TUN。 |
| 分应用/进程代理 | 不等价 | 提供设备 IPv4/IPv6 地址分流；无法从路由器侧识别终端 App/进程，find-process-mode=off。 |
| 托盘、全局快捷键、磁贴、Android TV | 平台专属 | 本插件运行在路由器软件中心，没有桌面/Android 客户端组件。 |
| 深链接导入与 Android 自动化 Action | 未实现/平台专属 | 目前通过网页导入；没有注册 clash:// 或 flclash://，也没有 Android Activity。 |
| 主题与交互 | 按用户要求适配软件中心 | 0.3.1 使用固件顶部/左侧菜单，ASUSWRT / ROG / TUF / TS 皮肤和紧凑横向功能标签；不复制 Material You 动态取色或手机吸附面板。 |
| 开机恢复 | 有路由器实现 | 通过软件中心 S98 / N98 / T98 钩子和持久启用状态恢复；还需 GT-AX6000 实机重启验收。 |

## 源码发现与 0.3.7 修复

| FlClash 源码证据 | 本插件之前的差距 | 本次处理 |
| --- | --- | --- |
| [profiles.dart 更新后重新读取当前配置](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/providers/actions/profiles.dart#L138) | 慢下载持有服务操作锁；解除锁后需要处理删除/编辑冲突 | 网络与验证移出锁；保存前比对当前状态和原文件；已删除/并发修改时拒绝旧结果；停止会取消处理，失败不计为自动重试。临时检查内核串行执行。 |
| [profile.dart 元数据合并](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/models/profile.dart) | 改名会丢失服务器返回的订阅额度 | 同一订阅地址保留服务端元数据，忽略客户端伪造额度；更换来源会清除旧额度；重新下载以新响应头为准。 |
| [proxies.dart 可取消任务与测试地址](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/providers/actions/proxies.dart) / [delay.go 有界工作池](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/core/delay.go) | 测试参数写死、无法取消、旧结果永久覆盖 | 界面提供 HTTP(S) 地址、1–15 秒超时、1–8 并发（默认4）；可取消尚未派发项，已开始项等返回或超时；结果按测试地址隔离，新的内核历史替代旧手动结果。自定义地址不冒用无地址标记的内核历史。测速仍是延迟，不是带宽。 |
| [proxies.dart 切换后连接处理](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/providers/actions/proxies.dart#L178) | 切线路后旧连接不变，用户可能误以为没切换 | 默认保留旧连接并明确提示；增加可选关闭全部旧连接，提示会中断现有下载/视频。 |
| [provider.go 外部集合更新](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/core/provider.go) | 只有逐个集合更新，缺少批量结果 | 增加规则/代理集合批量更新和每项成功/失败，部分失败不会显示全成功。 |
| [geo.go 周期管理](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/core/geo.go) | 仅能写 YAML 配置周期 | 「规则 → 规则与代理集合」提供 HTTP 集合周期、GEO 自动更新和周期；默认沿用配置，保存会应用设置并在运行时重启。0 表示继承，1–720 小时表示覆盖。修改周期不改变集合缓存身份；本地/内嵌集合不受影响。 |
| 本插件分块请求链路审查 | 上传完成任务还包含一个真正保存任务，前端会提前报成功 | 前端等待最终任务，最终保存失败向上传递；普通和分块请求均有回归测试。 |

GEO/集合调度字段遵循 [mihomo 通用配置](https://wiki.metacubex.one/config/general/)；运行时集合操作通过 [mihomo REST API](https://wiki.metacubex.one/api/)。更新计划不是更新成功状态：当前没有 GEO 逐资源事件回调、失败诊断或下次执行倒计时，不能保证上游下载在每次定时触发时成功。

## 仍未完成的差距

- **单节点分享链接与加密配置**：0.3.8 已提供可视化编辑；仍不能直接导入任意 ss://、vmess:// 等分享链接，未实现 FlClash 的 age 加密配置流程。
- **按配置单独覆写、JavaScript**：[scripts.dart](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/providers/actions/scripts.dart) 将脚本关联配置。本插件仅有全局 YAML 覆写，无 JS 运行环境，不能声称兼容 FlClash 脚本。
- **WebDAV 与备份兼容**：[backup.dart](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/providers/actions/backup.dart) 包含数据库快照及远端备份；本插件是自己的本地 ZIP 格式，未实现 WebDAV。
- **GEO 逐资源管理、完整 IP 质量分析、请求事件分析**：0.3.9 有网站/地区线索和 Cloudflare 域名对应的出口 IP 检查，但没有完整 IP 信誉/质量模型或 GEO 逐资源事件。当前集合结果、连接表、DNS 历史不能替代这些能力。
- **TUN 与终端平台功能**：现在是 REDIRECT/TPROXY，tun.enable=false；桌面托盘、快捷键、Android 磁贴和按终端 App/进程分流不适用于当前路由器管理端。

FlClash 使用其修改过的 mihomo 子模块及事件/回调；本插件使用官方 mihomo 二进制与 REST API。需要扩展内核事件接口的功能不能仅加按钮宣称完成。

## 本轮验证边界

- Go 全包测试、vet、前端类型检查/生产构建、安装包逐文件与 ARM64/内核校验通过。
- 新回归覆盖慢订阅期间停止、已删除配置不复活、并发设置修改不覆盖、额度保留/换源清除、排队检查取消、更新计划继承/覆盖/缓存身份，以及分块任务最终完成和失败传递。
- Windows 原生 mihomo 4 项回归通过：配置保存重启边界、本地集合备份恢复、在线缓存恢复隔离、USB 目录与持久集合缓存。是本机回环测试，不是路由器透明代理实测。
- 界面验证使用安装包实际 ASP/JS/CSS、拦截全部网络的虚拟固件和模拟 RPC；涵盖九页、四种皮肤、样式隔离及本次新操作。外层固件与数据为替身。
- **GT-AX6000 真机安装、TPROXY、IPv6、USB 异常拔盘、重启和长期内存仍待验收**。没有把历史授权等同于本轮已获得实机访问。

## 0.3.8 可视化编辑验收

对照同一固定提交的 [CustomProxy / ProxyGroup 模型与字段](https://github.com/chen08209/FlClash/blob/7fd417fb9a9a277c6a9339a8ac87f907df7c25f7/lib/models/clash_config.dart#L79)，并核对随包 mihomo v1.19.33 的 adapter/parser.go：提供 28 种协议类型入口、常用字段及单项高级 YAML。部分复杂协议仍需高级 YAML，未声称每种可选字段都有专用控件或所有协议已联网测试。

节点/组流程、保留未知字段/注释、引用同步、去重、循环检测、旧稿保护已有回归。实际编辑器生成的组配置通过本机 mihomo -t；原生内核测试确认错误编辑不替换当前文件或重启运行内核。安装包 UI 的编辑与保存失败重试流程使用全拦截模拟 RPC，不能替代 GT-AX6000 真机验收。

## 0.3.9 连通与节点卡片

概览包含接口状态、直连参考站点、常用站点 HTTPS 连通和手动流媒体页面信号检查。节点页支持本机测速内核与正式接管分离；自动策略组默认折叠，手动选择组优先。该模式是路由器侧适配，不是复制桌面系统代理开关。网站连通不代表各 LAN 设备透明代理完整验收；流媒体页面信号不是登录播放验收，详见 [说明](CONNECTIVITY.md)。
