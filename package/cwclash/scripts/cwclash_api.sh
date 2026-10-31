#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# softcenter supplies request id, action and one base64-encoded JSON payload.
export PATH=/koolshare/bin:/koolshare/scripts:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}
umask 077
CW_RUN=/tmp/cwclash-run
CW_BIN=${CW_RUN}/cwclash
case "$1" in ''|*[!0-9]*) exit 1 ;; esac
case "$2" in
  status|state.get|probe|storage.inspect|storage.cleanAsd|storage.undoAsd|jobs.get|service.start|service.stop|service.restart|service.nat|settings.save|devices.list|devices.save|profiles.save|profiles.read|profiles.select|profiles.update|profiles.delete|core.check|core.upgrade|core.rollback|upload.begin|upload.chunk|upload.finish|upload.cancel|download.begin|download.chunk|download.cancel|logs|dns.history|dns.query|groups|groups.select|groups.delay|connections|connections.close|rules|providers|providers.update|mode.set|cache.clear) ;;
  *) exit 1 ;;
esac
if [ ! -x "$CW_BIN" ] || [ ! -s "${CW_RUN}/ipc.json" ]; then
  /koolshare/scripts/cwclash_service.sh manager-start >/dev/null 2>&1
fi
CW_REPLY="${CW_RUN}/reply.$1"
if [ -x "$CW_BIN" ]; then
  "$CW_BIN" rpc "$2" "${3:-e30=}" > "$CW_REPLY"
else
  printf '%s' 'eyJvayI6ZmFsc2UsImVycm9yIjoiTWFuYWdlciBub3QgcnVubmluZyJ9' > "$CW_REPLY"
fi
# Post via a file so large responses never become oversized shell arguments.
curl --noproxy '*' -sS --connect-timeout 3 --max-time 10 -X POST --data-binary "@${CW_REPLY}" "http://127.0.0.1:3030/_resp/$1" >/dev/null 2>&1
rm -f "$CW_REPLY"
