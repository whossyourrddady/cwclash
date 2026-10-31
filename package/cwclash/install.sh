#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
export PATH=/koolshare/bin:/koolshare/scripts:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}
umask 077
set -e
CW_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CW_ROOT=/koolshare/cwclash
CW_VERSION=$(cat "${CW_DIR}/version")
CW_STORAGE_MODE=$(cat "${CW_DIR}/storage-mode")
case "$CW_STORAGE_MODE" in native|usb-jffs) ;; *) echo '安装包存储模式无效'; exit 1 ;; esac
echo "【长城 Clash】开始安装 ${CW_VERSION}"
[ -f /koolshare/scripts/base.sh ] && [ -x /usr/bin/skipd ] || { echo '需要带软件中心的 ASUSWRT/Merlin 固件'; exit 1; }
[ "$(uname -m)" = aarch64 ] || { echo '此包需要 ARM64 / aarch64 平台'; exit 1; }
CW_MODEL=$(nvram get productid)
[ "$CW_MODEL" = GT-AX6000 ] || { echo "首版实机目标为 GT-AX6000，当前型号 ${CW_MODEL} 未验证"; exit 1; }
CW_JFFS_TYPE=$(awk '$2=="/jffs"{kind=$3} END{print kind}' /proc/mounts)
case "$CW_JFFS_TYPE" in ubifs|jffs2|ext3|ext4) ;; *) echo '未找到有效的 JFFS 持久分区'; exit 1 ;; esac
tar -tzf "${CW_DIR}/bin/manager.tar.gz" >/dev/null 2>&1 || { echo '固件 tar 无法读取管理程序压缩包，未更改现有安装'; exit 1; }
cru l >/dev/null 2>&1 || { echo '固件 cru 无法读取任务列表，无法安排独立故障清理'; exit 1; }
echo '校验安装文件...'
. "${CW_DIR}/scripts/cwclash_compat.sh"
(cd "$CW_DIR" && cw_verify_manifest) || { echo '安装包文件校验失败，未更改现有安装'; exit 1; }

# Test the firmware's own tar decoder and manager before modifying JFFS.
# Software-center cleanup owns /tmp/cwclash; runtime must use a different path.
CW_CHECK=/tmp/cwclash-check.$$
mkdir "$CW_CHECK" || { echo '无法创建独立内存检查目录，未更改现有安装'; exit 1; }
trap 'rm -f "${CW_CHECK}/cwclash.next"; rmdir "$CW_CHECK" 2>/dev/null' 0
trap 'exit 1' 1 2 15
tar -xzf "${CW_DIR}/bin/manager.tar.gz" -C "$CW_CHECK" cwclash.next || { echo '管理程序解包失败，未更改现有安装'; exit 1; }
CW_EXPECTED=$(cat "${CW_DIR}/bin/manager.sha256")
CW_ACTUAL=$(cw_sha256 "${CW_CHECK}/cwclash.next") || exit 1
[ "$CW_ACTUAL" = "$CW_EXPECTED" ] || { echo '管理程序校验失败，未更改现有安装'; exit 1; }
chmod 700 "${CW_CHECK}/cwclash.next"
if CW_MANAGER_VERSION=$("${CW_CHECK}/cwclash.next" version 2>&1); then
  [ "$CW_MANAGER_VERSION" = "$CW_VERSION" ] || { echo "管理程序版本不匹配：包版本 [${CW_VERSION}]，程序返回 [${CW_MANAGER_VERSION}]，未更改现有安装"; exit 1; }
else
  CW_MANAGER_EXIT=$?
  echo "管理程序启动失败，退出码 ${CW_MANAGER_EXIT}：${CW_MANAGER_VERSION}；未更改现有安装"
  exit 1
fi

CW_RESERVE=4096
if [ "$CW_STORAGE_MODE" = usb-jffs ]; then
  echo '核验 USB2JFFS 挂载及插件目录...'
  for CW_TARGET in /koolshare "$CW_ROOT" /koolshare/scripts /koolshare/webs /koolshare/res/cwclash /koolshare/init.d; do
    "${CW_CHECK}/cwclash.next" storage-check --root "$CW_TARGET" --mode usb-jffs || exit 1
  done
  CW_RESERVE=131072
fi
CW_FREE=$(df -Pk /koolshare | awk 'NR==2{print $4}')
CW_NEED=$(du -sk "$CW_DIR" | awk '{print $1}')
CW_REUSE=0
if [ -f "${CW_ROOT}/bin/manager.tar.gz" ] && [ ! -L "${CW_ROOT}/bin/manager.tar.gz" ]; then
  CW_REUSE=$(du -sk "${CW_ROOT}/bin/manager.tar.gz" | awk '{print $1}')
fi
CW_NEED=$((CW_NEED-CW_REUSE))
[ "$CW_NEED" -ge 0 ] || CW_NEED=0
[ -n "$CW_FREE" ] && [ "$CW_FREE" -gt $((CW_NEED+CW_RESERVE)) ] || { echo "目标存储空间不足，安装后需至少保留 $((CW_RESERVE/1024)) MiB；未更改现有安装"; exit 1; }

if [ -x /koolshare/scripts/cwclash_service.sh ]; then /koolshare/scripts/cwclash_service.sh manager-stop || true; fi
mkdir -p "${CW_ROOT}/bin" "${CW_ROOT}/cores" "${CW_ROOT}/profiles" "${CW_ROOT}/data/providers" "${CW_ROOT}/assets" "${CW_ROOT}/licenses" /koolshare/res/cwclash
cp "${CW_DIR}/storage-mode" "${CW_ROOT}/storage-mode"
cp "${CW_DIR}/bin/manager.tar.gz" "${CW_ROOT}/bin/manager.tar.gz"
cp "${CW_DIR}/bin/manager.sha256" "${CW_ROOT}/bin/manager.sha256"
cp "${CW_DIR}/bin/manager-archive.sha256" "${CW_ROOT}/bin/manager-archive.sha256"
if [ -f "${CW_DIR}/bundled-kernel.json" ]; then
  cp "${CW_DIR}/cores/"* "${CW_ROOT}/cores/"
  cp "${CW_DIR}/bundled-kernel.json" "${CW_ROOT}/bundled-kernel.json"
fi
cp "${CW_DIR}/LICENSE" "${CW_ROOT}/LICENSE"
cp "${CW_DIR}/THIRD_PARTY.md" "${CW_ROOT}/THIRD_PARTY.md"
cp "${CW_DIR}/licenses/"* "${CW_ROOT}/licenses/"
cp "${CW_DIR}/scripts/"*.sh /koolshare/scripts/
cp "${CW_DIR}/uninstall.sh" /koolshare/scripts/uninstall_cwclash.sh
cp "${CW_DIR}/webs/Module_cwclash.asp" /koolshare/webs/Module_cwclash.asp
cp "${CW_DIR}/res/cwclash/"* /koolshare/res/cwclash/
cp "${CW_DIR}/res/icon-cwclash.png" /koolshare/res/icon-cwclash.png
chmod 600 "${CW_ROOT}/bin/manager.tar.gz" "${CW_ROOT}/bin/manager.sha256" "${CW_ROOT}/bin/manager-archive.sha256"
find "${CW_ROOT}/cores" -type f -exec chmod 600 '{}' ';'
rm -f "${CW_ROOT}/bin/cwclash" "${CW_ROOT}/bin/cwclash.gz"
chmod 755 /koolshare/scripts/cwclash_*.sh /koolshare/scripts/uninstall_cwclash.sh
chmod 644 /koolshare/webs/Module_cwclash.asp /koolshare/res/cwclash/* /koolshare/res/icon-cwclash.png
rm -f /koolshare/init.d/S98cwclash.sh /koolshare/init.d/N98cwclash.sh /koolshare/init.d/T98cwclash.sh /koolshare/init.d/T49cwclash.sh
cp "${CW_DIR}/init.d/"*.sh /koolshare/init.d/
chmod 755 /koolshare/init.d/S98cwclash.sh /koolshare/init.d/N98cwclash.sh /koolshare/init.d/T98cwclash.sh
dbus set softcenter_module_cwclash_install=1
dbus set softcenter_module_cwclash_name=cwclash
dbus set softcenter_module_cwclash_title='长城 Clash'
dbus set "softcenter_module_cwclash_version=${CW_VERSION}"
dbus set softcenter_module_cwclash_description='基于 mihomo 的路由器代理、规则分流与设备管理'
dbus set softcenter_module_cwclash_home_url=Module_cwclash.asp
dbus set "cwclash_version=${CW_VERSION}"
/koolshare/scripts/cwclash_service.sh manager-start
/tmp/cwclash-run/cwclash health --runtime /tmp/cwclash-run || { echo '管理服务接口检查失败'; exit 1; }
echo '【长城 Clash】安装完成。打开插件，导入 Clash/mihomo YAML 配置。'
if [ "$CW_STORAGE_MODE" = usb-jffs ]; then
  echo '已安装到 USB 持久存储；内核为原始文件，配置及规则缓存保留在 U 盘。运行程序驻留内存。'
else
  echo '内核压缩存储于 JFFS，运行时解压到内存。'
fi
