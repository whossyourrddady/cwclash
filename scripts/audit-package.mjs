// SPDX-License-Identifier: GPL-3.0-or-later
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { gunzipSync } from 'node:zlib'
import { basename, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'

const version = (await readFile('package/cwclash/version', 'utf8')).trim()
const bootstrap = process.argv.includes('--bootstrap')
const usb = process.argv.includes('--usb')
const base = `cwclash_hnd_v8_${usb ? 'usb_' : bootstrap ? 'bootstrap_' : ''}${version}`
const path = `dist/${base}.tar.gz`
const archive = await readFile(path)
const digest = bytes => createHash('sha256').update(bytes).digest('hex')
const text = bytes => bytes.toString('utf8').replace(/\0.*$/s, '')
const octal = bytes => parseInt(text(bytes).trim() || '0', 8)
function readTar(tar, prefix) {
 const files = new Map()
 for (let offset = 0; offset + 512 <= tar.length;) {
  const header = tar.subarray(offset, offset + 512)
  if (header.every(byte => byte === 0)) break
  const name = text(header.subarray(0, 100)), size = octal(header.subarray(124, 136)), mode = octal(header.subarray(100, 108))
  const type = String.fromCharCode(header[156] || 48)
  assert.ok(name.startsWith(prefix) && !name.includes('..') && !name.includes('\\') && !name.startsWith('/'), `Unsafe archive path: ${name}`)
  assert.ok(type === '0' || type === '5', `Unexpected archive entry type: ${name}`)
  assert.equal(octal(header.subarray(108, 116)), 0, 'UID must be root')
  assert.equal(octal(header.subarray(116, 124)), 0, 'GID must be root')
  assert.ok(offset + 512 + size <= tar.length, 'Truncated tar entry')
  if (type === '0') {
    assert.ok(!files.has(name), `Duplicate entry: ${name}`)
    const executable = name.endsWith('.sh')
    assert.equal(mode, executable ? 0o755 : 0o644, `Wrong POSIX mode: ${name}`)
    const bytes = tar.subarray(offset + 512, offset + 512 + size)
    if (name.endsWith('.sh')) assert.ok(!bytes.includes(13), `CRLF in shell script: ${name}`)
    files.set(name, bytes)
  }
  offset += 512 + Math.ceil(size / 512) * 512
 }
 return files
}
const files = readTar(gunzipSync(archive), 'cwclash/')
for (const name of ['storage-mode','init.d/S98cwclash.sh','init.d/N98cwclash.sh','init.d/T98cwclash.sh']) assert.ok(files.has('cwclash/'+name),`Missing ${name}`)
assert.equal(files.get('cwclash/storage-mode').toString().trim(),usb?'usb-jffs':'native')
for (const [name,action] of [['S98cwclash.sh','manager-start'],['N98cwclash.sh','start_nat'],['T98cwclash.sh','manager-stop']]) assert.ok(files.get('cwclash/init.d/'+name).toString().includes('cwclash_service.sh '+action),'Incorrect firmware hook')
for (const name of ['.valid', 'version', 'install.sh', 'uninstall.sh', 'bin/manager.tar.gz', 'bin/manager.sha256', 'bin/manager-archive.sha256', 'scripts/cwclash_service.sh', 'scripts/cwclash_api.sh', 'scripts/cwclash_compat.sh', 'webs/Module_cwclash.asp', 'res/cwclash/app.js', `res/cwclash/style-${version}.css`, 'res/icon-cwclash.png', 'LICENSE', 'THIRD_PARTY.md', 'licenses/Go-BSD-3-Clause.txt', 'licenses/Vue-MIT.txt', 'licenses/yaml-JS-ISC.txt', 'licenses/Vite-LICENSE.txt', 'licenses/yaml-v3-LICENSE.txt', 'licenses/xz-BSD-3-Clause.txt']) assert.ok(files.has('cwclash/' + name), `Missing required file: ${name}`)
assert.equal(files.get('cwclash/version').toString(), version + '\n', 'Version must use exactly one LF, without CR or whitespace')
assert.equal(files.get('cwclash/.valid').toString().trim(), 'hnd')
const checks = files.get('cwclash/CHECKSUMS.sha256').toString().trim().split('\n')
assert.equal(checks.length, files.size - 1, 'Some files are not covered by checksums')
const seen = new Set()
for (const line of checks) {
  const match = /^([a-f0-9]{64})  (.+)$/.exec(line)
  assert.ok(match, 'Malformed checksum line')
  const [, sha, name] = match
  assert.ok(!seen.has(name), `Repeated checksum: ${name}`); seen.add(name)
  assert.ok(files.has('cwclash/' + name), `Checksum refers to missing file: ${name}`)
  assert.equal(digest(files.get('cwclash/' + name)), sha, `File checksum mismatch: ${name}`)
}
function verifyELF(bytes, label) {
  assert.equal(bytes.subarray(0, 4).toString('hex'), '7f454c46', `${label} is not ELF`)
  assert.equal(bytes[4], 2); assert.equal(bytes[5], 1); assert.equal(bytes.readUInt16LE(18), 183)
  const start = Number(bytes.readBigUInt64LE(32)), entrySize = bytes.readUInt16LE(54), count = bytes.readUInt16LE(56)
  for (let i = 0; i < count; i++) assert.notEqual(bytes.readUInt32LE(start + entrySize * i), 3, `${label} requires a dynamic loader`)
}
const managerArchive = files.get('cwclash/bin/manager.tar.gz')
assert.equal(digest(managerArchive), files.get('cwclash/bin/manager-archive.sha256').toString().trim())
const managerFiles = readTar(gunzipSync(managerArchive), '')
assert.deepEqual([...managerFiles.keys()], ['cwclash.next'])
const manager = managerFiles.get('cwclash.next')
verifyELF(manager, 'Manager')
assert.equal(digest(manager), files.get('cwclash/bin/manager.sha256').toString().trim())
let kernel = null
if (bootstrap) {
  assert.ok(!files.has('cwclash/bundled-kernel.json'), 'Bootstrap must not silently bundle a core')
  assert.equal([...files.keys()].filter(name=>name.startsWith('cwclash/cores/')).length, 0)
} else {
  kernel = JSON.parse(files.get('cwclash/bundled-kernel.json'))
 if (usb) {
  assert.ok(!kernel.compression,'USB core must use original raw bytes')
  const core=files.get('cwclash/cores/'+kernel.file)
  verifyELF(core,'USB mihomo');assert.equal(digest(core),kernel.sha256)
 } else {
  assert.equal(kernel.compression,'xz')
  const archive = files.get('cwclash/cores/' + kernel.file)
  assert.ok(archive, 'Bundled kernel is missing'); assert.equal(digest(archive),kernel.archiveSHA256)
  await mkdir('build',{recursive:true})
  await writeFile('build/audit-core.xz',archive)
  const go = process.env.CWCLASH_GO || resolve('.tools/go/bin',process.platform==='win32'?'go.exe':'go')
  const result = spawnSync(go,['run','./cmd/cwxz','-d','build/audit-core.xz','build/audit-core-raw'],{stdio:'inherit',windowsHide:true,env:{...process.env,GOCACHE:resolve('.cache/go-build'),GOMODCACHE:resolve('.tools/gomod'),GOTMPDIR:resolve('.cache/tmp')}})
  if (result.error) throw result.error
  assert.equal(result.status,0,'Core XZ decoding failed')
  const core = await readFile('build/audit-core-raw'); verifyELF(core,'mihomo'); assert.equal(digest(core),kernel.sha256)
 }
}
const sha = digest(archive)
assert.equal((await readFile(`dist/${base}.sha256`, 'utf8')).trim(), `${sha}  ${basename(path)}`)
const asp = files.get('cwclash/webs/Module_cwclash.asp').toString()
assert.ok(asp.includes('Content-Security-Policy') && asp.includes('/res/cwclash/app.js'))
assert.ok(asp.includes('/res/cwclash/app.js?v='+version),'UI cache version is missing')
assert.ok(files.has('cwclash/res/cwclash/host.js'),'Firmware host adapter is missing')
for (const id of ['TopBanner','mainMenu','subMenu','tabMenu','FormTitle','footer','cwclash-app']) assert.ok(asp.includes('id="'+id+'"'),'Missing native firmware container: '+id)
assert.ok(asp.includes('/state.js') && asp.includes('/res/softcenter.css'),'Native firmware resources missing')
assert.ok(asp.includes('/res/cwclash/host.js?v='+version),'Host cache version is missing')
assert.ok(asp.includes('/res/cwclash/style-'+version+'.css'),'Versioned CSS filename missing')
assert.ok(!/href="[^"]+\.css\?/.test(asp),'Legacy firmware CSS URLs must not contain a query')
const report = { time: new Date().toISOString(), package: basename(path), bootstrap,storageMode:usb?'usb-jffs':'native', sha256: sha, bytes: archive.length, files: files.size, kernel: kernel?.version || null, checks: ['root directory', 'no traversal or symlinks', 'POSIX modes', 'LF shell scripts', 'complete SHA256 manifest', 'tar.gz manager roundtrip, archive and binary SHA256, static ELF64/AArch64', bootstrap ? 'no bundled core' : usb?'raw USB core original SHA256 and static ELF64/AArch64':'XZ core roundtrip, original SHA256 and static ELF64/AArch64', 'required licenses', 'local UI resources and CSP','explicit firmware hook arguments','storage layout marker'], routerAcceptance: false }
await mkdir('output/verification', { recursive: true })
await writeFile(`output/verification/package-audit${usb?'-usb':bootstrap?'-bootstrap':''}.json`, JSON.stringify(report, null, 2) + '\n')
console.log(JSON.stringify(report, null, 2))
