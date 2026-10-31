# mihomo 对应源码与构建材料

长城 Clash 0.3.9 USB 完整版随附的代理内核是 MetaCubeX/mihomo **v1.19.33 官方未修改的 Linux ARM64 二进制**。插件包中不含 Windows 内核。USB 包保存原始 ELF 文件，外层 tar.gz 仅用于传输；启动时校验后复制到内存执行。

对应提交：`3f27ef39f8c96bbfbc8b0b9bb10f5c9ef60149d8`。固定版本源码：[mihomo v1.19.33](https://github.com/MetaCubeX/mihomo/tree/v1.19.33)。许可证为 GNU GPL v3，全文随包提供。

`mihomo-v1.19.33-corresponding-source.tar.gz` 独立于路由器安装包，包含：

- `mihomo-source-v1.19.33.tar.gz`：上述提交的 GitHub 官方源码归档。
- `vendor.tar.gz`：该发布版提供的依赖源码。
- `toolchain.tar.gz`：该发布版提供的 Go 1.26.10 构建工具链。
- `SOURCES.lock.json`：下载地址、大小与 SHA-256。

依赖和工具链摘要与官方 GitHub 发布资产摘要一致。GitHub 源码归档绑定固定提交，其 SHA-256 在通过 HTTPS 下载后记录。该源码包供保存、审查或重建，**不要上传到路由器软件中心安装**。

在适合该工具链的 Linux 构建主机上，从本目录执行：

```sh
mkdir -p tools src
tar -xzf toolchain.tar.gz -C tools
tar -xzf mihomo-source-v1.19.33.tar.gz -C src --strip-components=1
tar -xzf vendor.tar.gz -C src
cd src
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  ../tools/go/bin/go build -mod=vendor -tags with_gvisor -trimpath \
  -ldflags '-X github.com/metacubex/mihomo/constant.Version=v1.19.33 -w -s -buildid=' \
  -o mihomo-linux-arm64 .
```

参数依据上游版本中的 `Makefile`：`CGO_ENABLED=0`、`with_gvisor`、`-trimpath`。这里不宣称生成的文件与官方发布二进制逐字节相同；官方编译时间、构建环境等可能不同。其他主机可安装相应平台的 Go 1.26.10，使用同一源码与 vendor 构建。

原创管理程序、界面、安装脚本和构建脚本另见 `cwclash-source-0.3.9.tar.gz`。其中包含 Go YAML 与 XZ 依赖源码；前端依赖由 `web/package-lock.json` 固定，可用 `npm ci` 安装。使用 `node scripts/build.mjs --usb` 构建 USB 完整包。
