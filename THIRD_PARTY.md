# 开源组件与来源

长城 Clash 的原创代码以 GPL-3.0-or-later 提供。`LICENSE` 为 GNU GPL 第三版全文。

| 组件 | 用途 | 许可证 | 对应源码 |
| --- | --- | --- | --- |
| mihomo v1.19.33 | 代理内核，使用官方未修改二进制 | GPL-3.0 | https://github.com/MetaCubeX/mihomo/tree/v1.19.33 |
| Vue 3.5.44 | 浏览器端界面 | MIT | https://github.com/vuejs/core |
| yaml 2.9.1 | 浏览器端保留 YAML 结构的可视化编辑 | ISC | https://github.com/eemeli/yaml/tree/v2.9.1 |
| yaml.v3 v3.0.1 | 本地 YAML 解析与生成 | MIT / Apache-2.0 | https://github.com/go-yaml/yaml/tree/v3.0.1 |
| ulikunitz/xz v0.5.15 | 核心压缩存储及内存解压 | BSD-3-Clause | https://github.com/ulikunitz/xz/tree/v0.5.15 |
| Go | 静态编译工具链和标准库 | BSD-3-Clause | https://go.dev/ |

Vue、Go 模块各自的许可证随构建产物一并收集至 `licenses` 目录。

构建资源版本、官方下载地址、文件大小及 SHA-256 记录在 `dependencies/mihomo.lock.json`。mihomo 的对应源码可从该版本标签获取，构建参数参见上游 `Makefile`；上游同一发布页还提供 `vendor.tar.gz` 与 `toolchain.tar.gz`。发布源码包时同时附上对应内核源码归档及上游构建材料的获取信息。

参考项目：

- FlClash：参考配置、策略组和实时连接管理的使用方式；没有复制其 Flutter 应用代码、商标或素材。
- koolshare/rogsoft：遵循其软件中心离线包结构及认证接口。USB2JFFS 使用官方插件独立完成，长城 Clash 不替换其挂载脚本。
- fancyss：参考 GT-AX6000 适配范围和路由器代理能力，不读写其节点数据库或业务配置。
