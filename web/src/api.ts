export type Envelope<T> = { ok: boolean; data?: T; error?: string }
export type Job = { id: string; kind: string; status: 'running' | 'done' | 'error'; message: string; result?: unknown }
export type Profile = { id: string; name: string; url?: string; userAgent?: string; updateHours: number; updatedAt?: string; subscriptionInfo?: string }
export type DevicePolicy = { id: string; name: string; ip: string; mac?: string; mode: 'direct' | 'rule' | 'proxy'; group?: string }
export type Settings = {
  lanInterface: string; mixedPort: number; redirPort: number; tproxyPort: number; dnsPort: number; controllerPort: number
  dnsMode: 'fake-ip' | 'redir-host'; ipv6: 'auto' | 'proxy' | 'block'; blockQuic: boolean; defaultGroup: string
  directDomains: string[]; proxyDomains: string[]; directCidrs: string[]; proxyCidrs: string[]; customRules: string[]
  overrideYaml: string; mode: 'rule' | 'global' | 'direct'; logLevel: string
}
export type Kernel = { version: string; file: string; sha256: string; previous?: Kernel }
export type State = { schema: number; enabled: boolean; activeProfile: string; profiles: Profile[]; devices: DevicePolicy[]; settings: Settings; kernel: Kernel }
export type Status = { running: boolean; enabled: boolean; version: string; kernel: Kernel; activeProfile: string; mode: string; uptime: number; error?: string; dev: boolean; storageMode?: string; uploadTotal?: number; downloadTotal?: number; connectionCount?: number; memory?: number; network: { lanInterface: string; ipv6: string; dnsMode: string; failOpen: boolean; mixedPort: number } }

function encode(value: unknown): string {
  const bytes = new TextEncoder().encode(JSON.stringify(value))
  let binary = ''
  for (let i = 0; i < bytes.length; i += 8192) binary += String.fromCharCode(...bytes.subarray(i, i + 8192))
  return btoa(binary)
}

function decode(value: string): unknown {
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(value), c => c.charCodeAt(0))))
}

export async function rpc<T = any>(action: string, payload: unknown = {}): Promise<T> {
  const encoded = encode(payload)
  // Older httpdb builds accept at most eight ID digits and a 4096-byte
  // command buffer. Keep both base64 layers of file chunks below that limit.
  if (encoded.length > 1600) {
    if (!['profiles.save', 'settings.save', 'devices.save'].includes(action)) throw new Error('请求参数过长，请缩短名称或使用文件导入')
    return uploadFile('request', new File([JSON.stringify(payload)], 'request.json'), { action }, () => undefined)
  }
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 100_000)
  try {
    const response = await fetch('/_api/', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
      body: JSON.stringify({ id: Math.floor(Math.random() * 99999999) + 1, method: 'cwclash_api.sh', params: [action, encoded], fields: {} }),
    })
    if (!response.ok) throw new Error(`软件中心接口 HTTP ${response.status}`)
    const outer = await response.json()
    if (typeof outer.result !== 'string' || !outer.result) throw new Error('软件中心未返回插件数据，请刷新页面确认插件版本；接口脚本或请求编号不兼容时会出现此错误')
    const result = decode(outer.result) as Envelope<T>
    if (!result.ok) throw new Error(result.error || '操作失败')
    return result.data as T
  } finally { clearTimeout(timeout) }
}

export async function waitJob(job: { jobId: string }, onMessage?: (message: string) => void): Promise<any> {
  for (let count = 0; count < 300; count++) {
    const state = await rpc<Job>('jobs.get', { id: job.jobId })
    onMessage?.(state.message)
    if (state.status === 'error') throw new Error(state.message)
    if (state.status === 'done') return state.result
    await new Promise(resolve => setTimeout(resolve, 700))
  }
  throw new Error('操作仍在运行，请稍后查看状态；不要重复提交')
}

export async function actionJob(action: string, payload: unknown = {}, onMessage?: (message: string) => void): Promise<any> {
  return waitJob(await rpc<{ jobId: string }>(action, payload), onMessage)
}

export async function uploadFile(kind: string, file: File, metadata: unknown, progress: (value: number) => void): Promise<any> {
  const start = await rpc<{ id: string; chunkSize: number }>('upload.begin', { kind, name: file.name, size: file.size, metadata })
  const chunkSize = Math.min(start.chunkSize, 768)
  let completed = false
  try {
    for (let offset = 0; offset < file.size; offset += chunkSize) {
      const data = new Uint8Array(await file.slice(offset, offset + chunkSize).arrayBuffer())
      let binary = ''; for (const byte of data) binary += String.fromCharCode(byte)
      await rpc('upload.chunk', { id: start.id, offset, data: btoa(binary) })
      progress(Math.round(Math.min(offset + data.length, file.size) / file.size * 100))
    }
    const job = await rpc<{ jobId: string }>('upload.finish', { id: start.id })
    completed = true
    return await waitJob(job)
  } finally { if (!completed) await rpc('upload.cancel', { id: start.id }).catch(() => undefined) }
}

export async function downloadFile(kind: string, profileId = ''): Promise<void> {
  const info = await rpc<{ id: string; name: string; size: number }>('download.begin', { kind, id: profileId })
  try {
    const chunks: Uint8Array<ArrayBuffer>[] = []
    let offset = 0
    while (offset < info.size) {
      const part = await rpc<{ data: string; offset: number }>('download.chunk', { id: info.id, offset })
      if (part.offset <= offset) throw new Error('下载中断')
      chunks.push(Uint8Array.from(atob(part.data), c => c.charCodeAt(0)))
      offset = part.offset
    }
    const blob = new Blob(chunks, { type: kind === 'backup' ? 'application/zip' : 'application/yaml' })
    const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = info.name; link.click(); setTimeout(() => URL.revokeObjectURL(url), 10_000)
  } finally { await rpc('download.cancel', { id: info.id }).catch(() => undefined) }
}
