import { createServer } from 'node:http'
import { readFile, mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { spawn } from 'node:child_process'
import process from 'node:process'

const root = resolve('.local/dev')
const runtime = resolve('.local/runtime')
await mkdir(root, { recursive: true }); await mkdir(runtime, { recursive: true })
const child = spawn(resolve('build/cwclash-windows.exe'), ['serve', '--root', root, '--runtime', runtime, '--dev'], { stdio: 'inherit', windowsHide: true })
const server = createServer(async (request, response) => {
  response.setHeader('Content-Type', 'application/json')
  response.setHeader('Cache-Control', 'no-store')
  if (request.method !== 'POST' || request.url !== '/_api/') { response.writeHead(404); response.end('{}'); return }
  try {
    const parts = []; let size = 0
    for await (const part of request) { size += part.length; if (size > 256 * 1024) throw Error('Request too large'); parts.push(part) }
    const outer = JSON.parse(Buffer.concat(parts).toString())
    if (outer.method !== 'cwclash_api.sh' || !Array.isArray(outer.params)) throw Error('Unknown method')
    if (!Number.isInteger(outer.id) || outer.id < 1 || outer.id > 99999999) throw Error('Request ID must have at most eight digits')
    const [action, encoded] = outer.params
    if (typeof encoded !== 'string' || encoded.length > 1600) throw Error('Request exceeds legacy software-center command limit')
    const payload = JSON.parse(Buffer.from(encoded, 'base64').toString())
    const ipc = JSON.parse(await readFile(resolve(runtime, 'ipc.json'), 'utf8'))
    if (!ipc.address.startsWith('127.0.0.1:')) throw Error('Not a loopback address')
    const res = await fetch(`http://${ipc.address}/rpc`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CWClash-Token': ipc.token }, body: JSON.stringify({ action, payload }) })
    const result = await res.text()
    response.end(JSON.stringify({ id: outer.id, result: Buffer.from(result).toString('base64') }))
  } catch (error) { response.end(JSON.stringify({ result: Buffer.from(JSON.stringify({ ok: false, error: error.message })).toString('base64') })) }
})
server.listen(17922, '127.0.0.1', () => console.log('cwclash development bridge http://127.0.0.1:17922'))
const shutdown = () => { server.close(); child.kill(); process.exit() }
process.on('SIGINT', shutdown); process.on('SIGTERM', shutdown)
child.on('exit', code => { if (code) console.error('Manager exit', code) })
