#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
export PATH=/koolshare/bin:/koolshare/scripts:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}
if [ -x /tmp/cwclash-run/cwclash ]; then /tmp/cwclash-run/cwclash rpc service.stop e30= >/dev/null 2>&1; sleep 2; fi
if [ -x /koolshare/scripts/cwclash_service.sh ]; then /koolshare/scripts/cwclash_service.sh manager-stop; fi
rm -f /koolshare/init.d/S98cwclash.sh /koolshare/init.d/N98cwclash.sh /koolshare/init.d/T98cwclash.sh /koolshare/init.d/T49cwclash.sh
rm -f /koolshare/webs/Module_cwclash.asp /koolshare/res/icon-cwclash.png
rm -f /koolshare/res/cwclash/app.js /koolshare/res/cwclash/style.css /koolshare/res/cwclash/icon.svg
rm -f /koolshare/cwclash/bin/cwclash /koolshare/cwclash/bin/cwclash.gz /koolshare/cwclash/bin/manager.tar.gz /koolshare/cwclash/bin/manager.sha256 /koolshare/cwclash/bin/manager-archive.sha256
for key in install name title version description home_url; do dbus remove "softcenter_module_cwclash_${key}"; done
dbus remove cwclash_version
rm -f /koolshare/scripts/cwclash_api.sh /koolshare/scripts/cwclash_service.sh /koolshare/scripts/cwclash_compat.sh
echo '长城 Clash 已卸载；配置、备份和内核保留于 /koolshare/cwclash，重新安装后可复用。'
