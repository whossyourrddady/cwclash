import assert from 'node:assert/strict'
import { createServer as httpServer, request } from 'node:http'
import { createServer as tcpServer, connect } from 'node:net'
import { createSocket } from 'node:dgram'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'

const report = []
let temporaryProfileID = '', originalProfileID = ''
async function rpc(action, payload = {}) {
  const response = await fetch('http://127.0.0.1:17922/_api/', { method: 'POST', body: JSON.stringify({ id: Math.floor(Math.random() * 99999999) + 1, method: 'cwclash_api.sh', params: [action, Buffer.from(JSON.stringify(payload)).toString('base64')] }) })
  const outer = await response.json(); const result = JSON.parse(Buffer.from(outer.result, 'base64').toString())
  if (!result.ok) throw Error(result.error)
  return result.data
}
async function job(action, payload = {}) {
  const start = await rpc(action, payload)
  for (let i = 0; i < 180; i++) {
    const state = await rpc('jobs.get', { id: start.jobId })
    if (state.status === 'error') throw Error(state.message)
    if (state.status === 'done') return state.result
    await new Promise(resolve => setTimeout(resolve, 150))
  }
  throw Error(`${action} timed out`)
}
async function check(name, fn) { await fn(); report.push({ name, passed: true }); console.log('PASS', name) }
async function listen(server) { return new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve(server.address().port))) }

const echo = httpServer((req, res) => { if (req.url === '/health') { res.writeHead(204); res.end(); return } res.end('cwclash-integration-ok') })
const echoPort = await listen(echo)
let socksConnections = 0
const socks = tcpServer(socket => {
  let phase = 0, buffered = Buffer.alloc(0)
  const onData = data => {
    buffered = Buffer.concat([buffered, data])
    if (phase === 0) { if (buffered.length < 2 || buffered.length < 2 + buffered[1]) return; buffered = buffered.subarray(2 + buffered[1]); socket.write(Buffer.from([5, 0])); phase = 1 }
    if (phase !== 1 || buffered.length < 4) return
    if (buffered[0] !== 5 || buffered[1] !== 1) { socket.destroy(); return }
    let host, offset
    if (buffered[3] === 1) { if (buffered.length < 10) return; host = [...buffered.subarray(4, 8)].join('.'); offset = 8 }
    else if (buffered[3] === 3) { const length = buffered[4]; if (buffered.length < 7 + length) return; host = buffered.subarray(5, 5 + length).toString(); offset = 5 + length }
    else { socket.destroy(); return }
    const port = buffered.readUInt16BE(offset), rest = buffered.subarray(offset + 2); phase = 2; socket.off('data', onData)
    const upstream = connect({ host, port }, () => { socksConnections++; socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 0])); if (rest.length) upstream.write(rest); socket.pipe(upstream); upstream.pipe(socket) })
    upstream.on('error', () => socket.destroy()); socket.on('error', () => upstream.destroy()); socket.on('close', () => upstream.destroy())
  }
  socket.on('data', onData); socket.on('error', () => undefined)
})
const socksPort = await listen(socks)

const upstreamDNS = createSocket('udp4')
upstreamDNS.on('message', (query, client) => {
  let end = 12
  while (end < query.length && query[end] !== 0) end += query[end] + 1
  end += 5
  if (end > query.length) return
  const header = Buffer.from(query.subarray(0, 12)); header[2] = 0x81; header[3] = 0x80
  const isA = query.readUInt16BE(end - 4) === 1
  header.writeUInt16BE(isA ? 1 : 0, 6); header.writeUInt16BE(0, 8); header.writeUInt16BE(0, 10)
  const answer = Buffer.from([0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 127, 0, 0, 1])
  upstreamDNS.send(Buffer.concat([header, query.subarray(12, end), ...(isA ? [answer] : [])]), client.port, client.address)
})
const dnsPort = await new Promise(resolve => upstreamDNS.bind(0, '127.0.0.1', () => resolve(upstreamDNS.address().port)))

function proxyGet(port) {
  return new Promise((resolve, reject) => {
    const req = request({ host: '127.0.0.1', port, path: `http://127.0.0.1:${echoPort}/echo`, headers: { Host: `127.0.0.1:${echoPort}` } }, res => { let text = ''; res.on('data', c => text += c); res.on('end', () => res.statusCode === 200 ? resolve(text) : reject(Error(`Proxy HTTP ${res.statusCode}: ${text}`))) })
    req.setTimeout(6000, () => req.destroy(Error('Proxy request timeout'))); req.on('error', reject); req.end()
  })
}

const yaml = `dns:\n  nameserver: [udp://127.0.0.1:${dnsPort}]\nproxies:\n  - name: Loopback-SOCKS\n    type: socks5\n    server: 127.0.0.1\n    port: ${socksPort}\nproxy-groups:\n  - name: Route\n    type: select\n    proxies: [DIRECT, Loopback-SOCKS]\nrules:\n  - MATCH,Route\n`
try {
  const initial = await rpc('state.get'); assert.equal(initial.dev, true, 'Integration tests must never run against router production mode')
  originalProfileID = initial.state.activeProfile
  if ((await rpc('status')).running) await job('service.stop')
  let profile
  await check('真实内核校验并保存 YAML', async () => { profile = await job('profiles.save', { name: '临时验收-'+Date.now(), content: yaml, updateHours: 0 }); temporaryProfileID = profile.id; await job('profiles.select', { id: profile.id }) })
  await check('启动真实 mihomo 并读取版本', async () => { await job('service.start'); const state = await rpc('status'); assert.equal(state.running, true); assert.equal(state.kernel.version, 'v1.19.33') })
  const state = await rpc('state.get'), mixedPort = state.state.settings.mixedPort
  await rpc('groups.select', { group: 'Route', node: 'DIRECT' })
  await check('HTTP 混合代理端口转发实际请求', async () => assert.equal(await proxyGet(mixedPort), 'cwclash-integration-ok'))
  await check('策略组选择切换为 SOCKS5 并实际转发', async () => { await rpc('groups.select', { group: 'Route', node: 'Loopback-SOCKS' }); assert.equal(await proxyGet(mixedPort), 'cwclash-integration-ok'); assert.ok(socksConnections > 0); const groups = await rpc('groups'); assert.equal(groups.proxies.Route.now, 'Loopback-SOCKS') })
  await check('实际节点延迟测试', async () => { const result = await rpc('groups.delay', { group: 'Loopback-SOCKS', url: `http://127.0.0.1:${echoPort}/health` }); assert.ok(result.delay >= 0) })
  await check('错误配置不替换可用配置', async () => { await assert.rejects(job('profiles.save', { id: profile.id, name: 'broken', content: 'proxies: [oops' })); assert.equal((await rpc('profiles.read', { id: profile.id })).content, yaml); assert.equal((await rpc('status')).running, true) })
  await check('模式切换经内核接口生效', async () => { await rpc('mode.set', { mode: 'direct' }); assert.equal((await rpc('status')).mode, 'direct'); await rpc('mode.set', { mode: 'rule' }) })
  await check('DNS 解析诊断读取真实内核结果', async () => { const result = await rpc('dns.query', { name: 'fixture.invalid', type: 'A' }); assert.ok(JSON.stringify(result).includes('127.0.0.1')) })
  await check('UDP DNS 接管入口记录查询', async () => {
    const packet = Buffer.from([0, 17, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, ...Buffer.from('fixture'), 7, ...Buffer.from('invalid'), 0, 0, 1, 0, 1])
    const socket = createSocket('udp4')
    const reply = await new Promise((resolve, reject) => { const timeout = setTimeout(() => { socket.close(); reject(Error('DNS timeout')) }, 5000); socket.once('message', data => { clearTimeout(timeout); socket.close(); resolve(data) }); socket.send(packet, state.state.settings.dnsPort, '127.0.0.1') })
    assert.equal(reply[3] & 15, 0); assert.ok((await rpc('dns.history')).some(q => q.name === 'fixture.invalid'))
  })
  await check('内核意外退出由守护发现，NAT 事件恢复服务', async () => { const pid = Number((await readFile('.local/runtime/core.pid', 'utf8')).trim()); assert.ok(pid > 1); process.kill(pid); await new Promise(resolve => setTimeout(resolve, 1600)); const result = await rpc('status'); assert.equal(result.running, false); assert.match(result.error, /意外退出/); await job('service.nat'); assert.equal((await rpc('status')).running, true) })
  await check('配置备份可分块下载', async () => { const start = await rpc('download.begin', { kind: 'backup' }); const chunk = await rpc('download.chunk', { id: start.id, offset: 0 }); assert.ok(Buffer.from(chunk.data, 'base64').subarray(0, 2).equals(Buffer.from('PK'))); await rpc('download.cancel', { id: start.id }) })
  await check('停止后代理端口关闭', async () => { await rpc('groups.select', {group:'Route',node:'DIRECT'}); await job('service.stop'); assert.equal((await rpc('status')).running, false); await assert.rejects(proxyGet(mixedPort)) })
} finally {
  await job('service.stop').catch(() => undefined)
  if (temporaryProfileID) {
    if (originalProfileID && originalProfileID !== temporaryProfileID) await job('profiles.select', {id:originalProfileID}).catch(()=>undefined)
    await rpc('profiles.delete', {id:temporaryProfileID})
  }
  echo.closeAllConnections(); echo.close(); socks.close(); upstreamDNS.close()
  await mkdir('output/verification', { recursive: true })
  await writeFile(resolve('output/verification/native-windows-integration.json'), JSON.stringify({ platform: 'Windows native mihomo, dev mode: no system routing changes', time: new Date().toISOString(), report }, null, 2))
}
