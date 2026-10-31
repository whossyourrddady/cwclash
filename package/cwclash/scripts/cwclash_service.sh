#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
export PATH=/koolshare/bin:/koolshare/scripts:/usr/sbin:/usr/bin:/sbin:/bin${PATH:+:$PATH}
umask 077
CW_ROOT=/koolshare/cwclash
CW_RUN=/tmp/cwclash-run
CW_PID=${CW_RUN}/manager.pid
CW_LOCK=${CW_RUN}/manager.lock
. /koolshare/scripts/cwclash_compat.sh || exit 1
CW_STORAGE_MODE=$(cat "${CW_ROOT}/storage-mode" 2>/dev/null)
case "$CW_STORAGE_MODE" in native|usb-jffs) ;; *) logger -t cwclash '存储模式文件不可用'; exit 1 ;; esac

# Serialize boot, NAT and UI requests before copying or stopping the manager.
manager_lock() {
  mkdir -p "$CW_RUN" || return 1
  CW_LOCK_TRIES=0
  while ! mkdir "$CW_LOCK" 2>/dev/null; do
    CW_LOCK_OWNER=$(cat "${CW_LOCK}/pid" 2>/dev/null)
    case "$CW_LOCK_OWNER" in
      ''|*[!0-9]*)
        [ "$CW_LOCK_TRIES" -gt 1 ] && rmdir "$CW_LOCK" 2>/dev/null
        ;;
      *)
        if ! kill -0 "$CW_LOCK_OWNER" 2>/dev/null; then
          rm -f "${CW_LOCK}/pid"
          rmdir "$CW_LOCK" 2>/dev/null
        fi
        ;;
    esac
    CW_LOCK_TRIES=$((CW_LOCK_TRIES+1))
    [ "$CW_LOCK_TRIES" -lt 35 ] || { logger -t cwclash '服务操作锁超时'; return 1; }
    sleep 1
  done
  echo $$ > "${CW_LOCK}/pid"
  trap 'rm -f "${CW_LOCK}/pid"; rmdir "$CW_LOCK" 2>/dev/null' 0
  trap 'exit 1' 1 2 15
}

manager_alive() {
  [ -s "$CW_PID" ] || return 1
  CW_OLD_PID=$(cat "$CW_PID" 2>/dev/null)
  case "$CW_OLD_PID" in ''|*[!0-9]*) return 1 ;; esac
  [ -r "/proc/${CW_OLD_PID}/cmdline" ] || return 1
  tr '\000' ' ' < "/proc/${CW_OLD_PID}/cmdline" | grep -F "${CW_RUN}/cwclash serve" >/dev/null 2>&1
}

manager_start() {
  manager_alive && return 0
  [ -r "${CW_ROOT}/bin/manager.tar.gz" ] && [ -r "${CW_ROOT}/bin/manager.sha256" ] && [ -r "${CW_ROOT}/bin/manager-archive.sha256" ] || return 1
  mkdir -p "$CW_RUN" || return 1
  if [ -x "${CW_RUN}/cwclash" ]; then "${CW_RUN}/cwclash" rescue --runtime "$CW_RUN" >/dev/null 2>&1 || logger -t cwclash '旧接管规则清理未完成，请查看环境诊断'; fi
  CW_ARCHIVE_EXPECTED=$(cat "${CW_ROOT}/bin/manager-archive.sha256")
  CW_ARCHIVE_ACTUAL=$(cw_sha256 "${CW_ROOT}/bin/manager.tar.gz") || return 1
  [ "$CW_ARCHIVE_ACTUAL" = "$CW_ARCHIVE_EXPECTED" ] || { logger -t cwclash '管理程序压缩包校验失败'; return 1; }
  rm -f "${CW_RUN}/cwclash.next"
  tar -xzf "${CW_ROOT}/bin/manager.tar.gz" -C "$CW_RUN" cwclash.next || { rm -f "${CW_RUN}/cwclash.next"; return 1; }
  CW_EXPECTED=$(cat "${CW_ROOT}/bin/manager.sha256")
  CW_ACTUAL=$(cw_sha256 "${CW_RUN}/cwclash.next") || { rm -f "${CW_RUN}/cwclash.next"; return 1; }
  [ "$CW_ACTUAL" = "$CW_EXPECTED" ] || { rm -f "${CW_RUN}/cwclash.next"; logger -t cwclash '管理程序校验失败'; return 1; }
  chmod 700 "${CW_RUN}/cwclash.next"
  mv -f "${CW_RUN}/cwclash.next" "${CW_RUN}/cwclash" || return 1
  "${CW_RUN}/cwclash" storage-check --root "$CW_ROOT" --mode "$CW_STORAGE_MODE" > "${CW_RUN}/storage-check.log" 2>&1 || { logger -t cwclash '持久存储校验未通过，管理服务未启动'; return 1; }
  rm -f "${CW_RUN}/ipc.json"
  "${CW_RUN}/cwclash" serve --root "$CW_ROOT" --runtime "$CW_RUN" --storage-mode "$CW_STORAGE_MODE" > "${CW_RUN}/manager.log" 2>&1 &
  echo $! > "$CW_PID"
  CW_TRIES=0
  while [ "$CW_TRIES" -lt 10 ]; do
    if [ -s "${CW_RUN}/ipc.json" ]; then
      cru a cwclash_watchdog '* * * * * [ -x /tmp/cwclash-run/cwclash ] && /tmp/cwclash-run/cwclash watchdog --runtime /tmp/cwclash-run >/dev/null 2>&1' || logger -t cwclash '独立守护任务创建失败'
      return 0
    fi
    sleep 1
    CW_TRIES=$((CW_TRIES+1))
  done
  logger -t cwclash '管理服务未就绪，请检查 /tmp/cwclash-run/manager.log'
  return 1
}

manager_stop() {
  cru d cwclash_watchdog >/dev/null 2>&1
  if manager_alive; then
    kill -TERM "$CW_OLD_PID" 2>/dev/null
    CW_TRIES=0
    while manager_alive && [ "$CW_TRIES" -lt 20 ]; do sleep 1; CW_TRIES=$((CW_TRIES+1)); done
    if manager_alive; then kill -KILL "$CW_OLD_PID" 2>/dev/null; fi
  fi
  if [ -x "${CW_RUN}/cwclash" ]; then "${CW_RUN}/cwclash" rescue --runtime "$CW_RUN" >/dev/null 2>&1; fi
  rm -f "$CW_PID" "${CW_RUN}/ipc.json"
}

case "$1" in
  start|manager-start|services-start|stop|manager-stop|services-stop|restart|start_nat|nat-start) manager_lock || exit 1 ;;
  *) echo 'usage: cwclash_service.sh start|stop|restart|start_nat' >&2; exit 2 ;;
esac
case "$1" in
  start|manager-start|services-start) manager_start ;;
  stop|manager-stop|services-stop) manager_stop ;;
  restart) manager_stop; manager_start ;;
  start_nat|nat-start)
    if manager_alive; then "${CW_RUN}/cwclash" rpc service.nat e30= >/dev/null 2>&1; else manager_start; fi
    ;;
  *) echo 'usage: cwclash_service.sh start|stop|restart|start_nat' >&2; exit 2 ;;
esac
