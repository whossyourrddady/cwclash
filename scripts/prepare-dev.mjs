import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { createHash } from 'node:crypto'

const lock = JSON.parse(await readFile('dependencies/mihomo.lock.json', 'utf8'))
const source = resolve('.tools/mihomo/windows/mihomo-windows-amd64.exe')
const data = await readFile(source)
const sha256 = createHash('sha256').update(data).digest('hex')
const file = `core-${sha256.slice(0,24)}`
await mkdir('.local/dev/cores', { recursive: true })
await copyFile(source, resolve('.local/dev/cores', file))
await writeFile('.local/dev/bundled-kernel.json', JSON.stringify({ version: lock.version, file, sha256 }, null, 2))
console.log(`Local test kernel: ${lock.version}, binary SHA256 ${sha256}`)
