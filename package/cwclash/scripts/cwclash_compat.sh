#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Merlin ash may omit the command builtin, mktemp and sha256sum applets.
# Test real operations; retain SHA-256 verification using firmware OpenSSL.
cw_sha256() {
  if CW_HASH_RESULT=$(sha256sum "$1" 2>/dev/null); then
    CW_HASH_VALUE=$(printf '%s\n' "$CW_HASH_RESULT" | awk '{print $1}')
  elif CW_HASH_RESULT=$(openssl dgst -sha256 "$1" 2>/dev/null); then
    CW_HASH_VALUE=$(printf '%s\n' "$CW_HASH_RESULT" | awk '{print $NF}')
  else
    echo '无法计算 SHA-256：需要 sha256sum 或固件 OpenSSL，未跳过校验' >&2
    return 1
  fi
  case "$CW_HASH_VALUE" in ''|*[!0-9a-f]*) return 1 ;; esac
  [ "${#CW_HASH_VALUE}" -eq 64 ] || return 1
  printf '%s\n' "$CW_HASH_VALUE"
}

cw_verify_manifest() {
  [ -s CHECKSUMS.sha256 ] || return 1
  CW_VERIFY_COUNT=0
  while read -r CW_VERIFY_EXPECTED CW_VERIFY_NAME; do
    case "$CW_VERIFY_EXPECTED" in ''|*[!0-9a-f]*) return 1 ;; esac
    [ "${#CW_VERIFY_EXPECTED}" -eq 64 ] || return 1
    case "$CW_VERIFY_NAME" in ''|/*|*..*|*\\*) return 1 ;; esac
    [ -f "$CW_VERIFY_NAME" ] && [ ! -L "$CW_VERIFY_NAME" ] || return 1
    CW_VERIFY_ACTUAL=$(cw_sha256 "$CW_VERIFY_NAME") || return 1
    [ "$CW_VERIFY_ACTUAL" = "$CW_VERIFY_EXPECTED" ] || { echo "文件校验失败：${CW_VERIFY_NAME}" >&2; return 1; }
    CW_VERIFY_COUNT=$((CW_VERIFY_COUNT+1))
  done < CHECKSUMS.sha256
  [ "$CW_VERIFY_COUNT" -gt 0 ]
}
