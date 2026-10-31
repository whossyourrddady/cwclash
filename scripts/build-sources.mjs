// SPDX-License-Identifier: GPL-3.0-or-later
import { access, copyFile, cp, mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { basename, join, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'

const version = (await readFile('package/cwclash/version', 'utf8')).trim()
const go = process.env.CWCLASH_GO || resolve('.tools/go/bin', process.platform === 'win32' ? 'go.exe' : 'go')
await access(go)
await mkdir('dist', { recursive: true })
const env = { ...process.env, GOCACHE: resolve('.cache/go-build'), GOMODCACHE: resolve('.tools/gomod'), GOTMPDIR: resolve('.cache/tmp') }
function run(args) {
  const result = spawnSync(go, args, { stdio: 'inherit', windowsHide: true, env })
  if (result.error) throw result.error
  if (result.status !== 0) throw Error(`Go command failed (${result.status})`)
}
const digest = data => createHash('sha256').update(data).digest('hex')
const stage = resolve(`build/sources-${Date.now()}`)
const source = join(stage, `cwclash-source-${version}`)
await mkdir(source, { recursive: true })
for (const file of ['.gitignore', '.gitattributes', 'LICENSE', 'README.md', 'THIRD_PARTY.md', 'go.mod', 'go.sum']) await copyFile(file, join(source, file))
for (const dir of ['cmd', 'internal', 'package', 'scripts', 'dependencies', 'docs']) await cp(dir, join(source, dir), { recursive: true, dereference: false, filter:path=>basename(path)!=='WORKSTATE.md' })
await cp('web', join(source, 'web'), { recursive: true, filter: path => !['node_modules', 'dist'].includes(basename(path)) })
run(['mod', 'vendor', '-o', join(source, 'vendor')])
const pluginArchive = resolve('dist', `cwclash-source-${version}.tar.gz`)
run(['run', './cmd/cwpack', stage, pluginArchive])

const lock = JSON.parse(await readFile('dependencies/upstream-sources.lock.json', 'utf8'))
const upstreamStage = resolve(`build/upstream-sources-${Date.now()}`)
const upstream = join(upstreamStage, `mihomo-${lock.version}-corresponding-source`)
await mkdir(upstream, { recursive: true })
for (const asset of lock.assets) {
  const from = resolve('.tools/sources', asset.name), bytes = await readFile(from)
  if (bytes.length !== asset.size || digest(bytes) !== asset.sha256) throw Error(`Upstream source checksum mismatch: ${asset.name}`)
  await copyFile(from, join(upstream, asset.name))
}
await copyFile('dependencies/upstream-sources.lock.json', join(upstream, 'SOURCES.lock.json'))
await copyFile('docs/CORE-SOURCE.md', join(upstream, 'README.md'))
await copyFile('LICENSE', join(upstream, 'LICENSE'))
const upstreamArchive = resolve('dist', `mihomo-${lock.version}-corresponding-source.tar.gz`)
run(['run', './cmd/cwpack', upstreamStage, upstreamArchive])
for (const path of [pluginArchive, upstreamArchive]) {
  const bytes = await readFile(path), sha = digest(bytes)
  await writeFile(path + '.sha256', `${sha}  ${basename(path)}\n`)
  console.log(`Built ${basename(path)} (${bytes.length} bytes), SHA256 ${sha}`)
}

const checks = []
for (const name of (await readdir('dist')).sort()) {
  if (name.endsWith('.gz')) checks.push(`${digest(await readFile(join('dist', name)))}  ${name}`)
}
await writeFile('dist/SHA256SUMS', checks.join('\n') + '\n')
