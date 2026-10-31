<script setup lang="ts">
import { ref, computed, reactive, onMounted, onUnmounted } from 'vue'
import Icon from './Icon.vue'
import ProxyEditor from './ProxyEditor.vue'
import {readCatalog} from './proxy-editor'
import { subscriptionSummary, orderNodes, currentDelay, historyKey, defaultTestURL } from './presentation'
import { rpc, actionJob, uploadFile, downloadFile } from './api'
import type { MeasuredDelay } from './presentation'
import type { State, Status, Settings, Profile, DevicePolicy } from './api'

const TextEncoder = globalThis.TextEncoder

type Page = 'overview' | 'profiles' | 'proxies' | 'rules' | 'connections' | 'dns' | 'devices' | 'logs' | 'settings'
const pages: { id: Page; label: string; icon: string; subtitle: string }[] = [
  { id: 'overview', label: '概览', icon: 'home', subtitle: '网络状态，一目了然' },
  { id: 'profiles', label: '配置', icon: 'folder', subtitle: '管理你的订阅与配置' },
  { id: 'proxies', label: '节点', icon: 'globe', subtitle: '节点、代理组与延迟测试' },
  { id: 'rules', label: '规则', icon: 'rules', subtitle: '让每一次连接各行其道' },
  { id: 'connections', label: '连接', icon: 'network', subtitle: '查看正在发生的网络连接' },
  { id: 'dns', label: 'DNS', icon: 'search', subtitle: '查询记录与解析诊断' },
  { id: 'devices', label: '设备', icon: 'shield', subtitle: '为每台设备选择联网方式' },
  { id: 'logs', label: '日志', icon: 'logs', subtitle: '了解内核运行情况' },
  { id: 'settings', label: '设置', icon: 'settings', subtitle: '内核、网络与数据管理' },
]
const page = ref<Page>((location.hash.slice(1) as Page) || 'overview')
if (!pages.some(p => p.id === page.value)) page.value = 'overview'
const current = computed(() => pages.find(p => p.id === page.value)!)
const model = document.querySelector('meta[name="cwclash-model"]')?.getAttribute('content') || 'ASUSWRT'
const state = ref<State | null>(null)
const status = ref<Status | null>(null)
const settings = ref<Settings | null>(null)
const busy = ref('')
const connected = ref(false)
const error = ref('')
const notice = ref<{ text: string; kind: string } | null>(null)
const search = ref('')
const progress = ref(0)
const proxyData = ref<Record<string, any>>({})
const proxySource = ref('')
const previewProviders = ref<string[]>([])
const providerPreviews = ref<any[]>([])
const nextUpdates = ref<Record<string,string>>({})
const delayResults = ref<Record<string,MeasuredDelay>>({})
const nodePrefs = reactive(readNodePrefs())
const batchActive = ref(false)
const batchCancelled = ref(false)
const stopPending = ref(false)
const providerResults = ref<Record<string,string>>({})
function readNodePrefs() {
  const fallback = {url: defaultTestURL, timeout: 5000, concurrency: 4, closeConnections: false}
  try { const value = {...fallback, ...JSON.parse(localStorage.getItem('cwclash.nodePrefs') || '{}')}; validateNodePrefs(value); return value } catch { return fallback }
}
function validateNodePrefs(value = nodePrefs) {
  const u = new URL(value.url)
  if (!['http:','https:'].includes(u.protocol) || value.timeout < 1000 || value.timeout > 15000 || !Number.isInteger(value.timeout) || !Number.isInteger(value.concurrency) || value.concurrency < 1 || value.concurrency > 8) throw new Error('请填写 HTTP(S) 测速地址、1000–15000 毫秒超时和 1–8 个并发')
}
function saveNodePrefs() {
  try {
    validateNodePrefs()
    localStorage.setItem('cwclash.nodePrefs',JSON.stringify(nodePrefs)); delayResults.value = {}; toast('节点操作偏好已保存在当前浏览器')
  } catch (e:any) { toast(e.message || '无法保存偏好','error') }
}
const batchSummary = ref('')
let lastProfile = ''
let groupsReadAt = 0
const canUseNodes = computed(() => connected.value && !!status.value?.running && proxySource.value === 'core')
const connections = ref<any[]>([])
const rules = ref<any[]>([])
const providers = ref<any>({})
const devices = ref<any[]>([])
const policies = ref<DevicePolicy[]>([])
const logs = ref<any[]>([])
const dnsHistory = ref<any[]>([])
const pauseLogs = ref(false)
const logLevel = ref('all')
const editingProfile = ref(false)
const profileContent = ref('')
const profileRevision = ref('')
const visualEditor = ref<{content:string;profile:Profile;revision:string}|null>(null)
const visualProtected = computed(() => {
  if(!visualEditor.value)return []
  const c=readCatalog(visualEditor.value.content),text=JSON.stringify({settings:state.value?.settings,devices:state.value?.devices})
  return [...c.proxies,...c.groups].map(p=>p.name).filter(n=>text.includes(n))
})
async function openVisual(profile?:Profile) {
  if(!profile)return
  await run('正在读取节点与代理组',async()=>{const data=await rpc('profiles.read',{id:profile.id});readCatalog(data.content);visualEditor.value={content:data.content,revision:data.revision,profile:data.profile}},'配置已载入可视化编辑器')
}
function saveVisual(data:{content:string;asCopy:boolean;name:string}) {
  void run('正在校验可视化编辑',async()=>{
    const original=visualEditor.value!
    const metadata=data.asCopy?{id:'',name:data.name,url:'',userAgent:'',updateHours:0}:{...original.profile,revision:original.revision}
    await uploadFile('profile',new File([data.content],'profile.yaml'),metadata,p=>progress.value=p)
    visualEditor.value=null
  }, data.asCopy?'本地副本已保存，请在配置页选择使用':'节点与代理组已通过检查并保存')
}
const profileFile = ref<File | null>(null)
const assetPath = ref('')
const formProfile = reactive<Profile>({ id: '', name: '', url: '', userAgent: '', updateHours: 24 })
const ruleForm = reactive({ directDomains: '', proxyDomains: '', directCidrs: '', proxyCidrs: '', customRules: '' })
const ruleTab = ref('active')
const dnsName = ref('')
const dnsType = ref('A')
const dnsResult = ref<any>(null)
const release = ref<any>(null)
const probe = ref<any>(null)
const storageInfo = ref<any>(null)
const showAdvanced = ref(false)
const settingsTab = ref('network')
const nodeSort = ref('default')
const nodeFilter = ref('all')
const favorites = ref<string[]>(readFavorites())
const settingsBaseline = ref('')
const devicesBaseline = ref('')
const settingsDraft = () => JSON.stringify({ settings: settings.value, rules: ruleForm })
const settingsDirty = computed(() => !!settingsBaseline.value && settingsDraft() !== settingsBaseline.value)
const devicesDirty = computed(() => !!devicesBaseline.value && JSON.stringify(policies.value) !== devicesBaseline.value)
function readFavorites(): string[] { try { const data = JSON.parse(localStorage.getItem('cwclash.favorites') || '[]'); return Array.isArray(data) ? data.filter(x => typeof x === 'string').slice(0, 2000) : [] } catch { return [] } }
function favoriteKey(group: string, node: string) { return JSON.stringify([state.value?.activeProfile, group, node]) }
function isFavorite(group: string, node: string) { return favorites.value.includes(favoriteKey(group, node)) }
function toggleFavorite(group: string, node: string) {
  const key = favoriteKey(group, node)
  favorites.value = isFavorite(group, node) ? favorites.value.filter(x => x !== key) : [...favorites.value, key].slice(-2000)
  try { localStorage.setItem('cwclash.favorites', JSON.stringify(favorites.value)) } catch { toast('浏览器无法保存收藏，本次页面内仍可使用', 'error') }
}
function visibleNodes(group: any): string[] { return orderNodes(group.all.filter((n: string) =>
  (!search.value || group.name.toLowerCase().includes(search.value.toLowerCase()) || n.toLowerCase().includes(search.value.toLowerCase())) &&
  (nodeFilter.value !== 'favorites' || isFavorite(group.name, n)) && (nodeFilter.value !== 'available' || delay(n) !== 0)), nodeSort.value, delay) }
function beforeUnload(event: BeforeUnloadEvent) { if (settingsDirty.value || devicesDirty.value) { event.preventDefault(); event.returnValue = '' } }
function discardDrafts() { ask('放弃未保存修改', '重新读取已保存的网络、规则和设备设置。', () => void run('正在还原设置', () => loadState('all'))) }

const confirmation = ref<{ title: string; text: string; confirm: () => void } | null>(null)
const rate = ref({ up: 0, down: 0 })
const samples = ref<number[]>(Array(32).fill(0))
let lastTraffic: { time: number; up: number; down: number } | null = null
let polling = false
let timer: ReturnType<typeof setInterval>
let toastTimer: ReturnType<typeof setTimeout>

const profileName = computed(() => state.value?.profiles.find(p => p.id === state.value?.activeProfile)?.name || '尚未选择配置')
const capturing = computed(() => connected.value && !!status.value?.running && status.value.mode !== 'direct')
const serviceLabel = computed(() => !connected.value ? '状态待确认' : capturing.value ? '局域网代理已启用' : status.value?.running ? '直连模式 · 内核运行中' : '代理已停止 · 当前使用直连')
const heroDescription = computed(() => !connected.value ? '管理服务连接中断，暂时无法确认路由器的代理状态。请重新连接后查看。' : capturing.value ? `正在使用「${profileName.value}」，为局域网设备提供${modeLabel(status.value?.mode)}分流。` : status.value?.running ? '局域网设备直接联网，mihomo 保留供手动代理使用。' : '导入 Clash 配置，选择适合的线路，即可开启路由器代理。')
const groups = computed(() => Object.entries(proxyData.value).filter(([, p]) => Array.isArray(p.all)).map(([name, p]) => ({ ...p, name })))
const displayGroups = computed(() => {
  const result = groups.value
  if (proxySource.value !== 'config') return result
  const members = new Set(result.flatMap(g => g.all))
  const nodes = Object.keys(proxyData.value).filter(n => !Array.isArray(proxyData.value[n].all) && !members.has(n))
  return nodes.length ? [...result, { name: '配置中的其他节点', type: '节点列表', all: nodes, catalog: true }] : result
})
const groupNames = computed(() => groups.value.map(g => g.name))
const expandedGroups = ref<Record<string, boolean>>({})
const filteredConnections = computed(() => connections.value.filter(c => JSON.stringify(c.metadata || {}).toLowerCase().includes(search.value.toLowerCase())))
const filteredRules = computed(() => rules.value.filter(r => `${r.type} ${r.payload} ${r.proxy}`.toLowerCase().includes(search.value.toLowerCase())))
const filteredLogs = computed(() => logs.value.filter(l => (logLevel.value === 'all' || l.level === logLevel.value) && l.message.toLowerCase().includes(search.value.toLowerCase())))
const filteredDNS = computed(() => dnsHistory.value.filter(q => `${q.name} ${q.client}`.toLowerCase().includes(search.value.toLowerCase())))
const graphPoints = computed(() => {
  const max = Math.max(1024, ...samples.value)
  return samples.value.map((n, i) => `${i * 100 / 31},${42 - n / max * 36}`).join(' ')
})

function toast(text: string, kind = 'success') { notice.value = { text, kind }; clearTimeout(toastTimer); toastTimer = setTimeout(() => notice.value = null, 7000) }
function navigate(id: Page) { page.value = id; location.hash = id; search.value = ''; void refreshPage().catch(() => undefined) }
function onHashChange() { const next = location.hash.slice(1) as Page; if (pages.some(p => p.id === next) && next !== page.value) { page.value = next; search.value = ''; void refreshPage().catch(() => undefined) } }
function bytes(n = 0) { if (!Number.isFinite(n) || n < 0) n = 0; const units = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i++ } return `${n.toFixed(i ? 1 : 0)} ${units[i]}` }
function date(value?: string) { if (!value) return '未更新'; const d = new Date(value); return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false }) }
function time(value?: string) { return value ? new Date(value).toLocaleTimeString('zh-CN', { hour12: false }) : '—' }
function lines(value: string) { return value.split('\n').map(s => s.trim()).filter(Boolean) }
function delay(name: string): number | null { return currentDelay(delayResults.value[name], proxyData.value[name]?.history, nodePrefs.url) }
function modeLabel(mode?: string) { return ({ rule: '规则', global: '全局', direct: '直连' } as Record<string, string>)[mode || ''] || '规则' }
function ipv6Label(mode?: string) { return ({ auto: '自动', proxy: '透明代理', block: '阻止外网直出' } as Record<string, string>)[mode || ''] || mode || '—' }
function ask(title: string, text: string, fn: () => void) { confirmation.value = { title, text, confirm: () => { confirmation.value = null; fn() } } }

async function run(label: string, fn: () => Promise<any>, success = '已完成') {
  if (busy.value) return
  busy.value = label; progress.value = 0
  try { const result = await fn(); toast(success); await loadState(); await refreshPage(); return result }
  catch (e: any) { toast(e.message || String(e), 'error') }
  finally { busy.value = '' }
}

async function loadState(reset = '') {
  groupsReadAt = 0
  const data = await rpc<{ state: State; nextUpdates?: Record<string,string> }>('state.get'); state.value = data.state; nextUpdates.value = data.nextUpdates || {}
  if (lastProfile !== data.state.activeProfile) { delayResults.value = {}; batchSummary.value = ''; proxyData.value = {}; providerPreviews.value = []; lastProfile = data.state.activeProfile }
  if (!settingsDirty.value || reset === 'settings' || reset === 'all') {
  settings.value = {geoAutoUpdate: null, geoUpdateHours: 0, providerUpdateHours: 0, ...structuredClone(data.state.settings)}

  for (const key of Object.keys(ruleForm) as (keyof typeof ruleForm)[]) ruleForm[key] = (data.state.settings[key] || []).join('\n')
  settingsBaseline.value = settingsDraft()
  }
  if (!devicesDirty.value || reset === 'devices' || reset === 'all') { policies.value = structuredClone(data.state.devices || []); devicesBaseline.value = JSON.stringify(policies.value) }
  status.value = await rpc<Status>('status'); connected.value = true; error.value = ''
}

async function refreshPage() {
  if (!status.value) return
  if (page.value === 'profiles') return
  if (page.value === 'devices') devices.value = await rpc('devices.list')
  if (page.value === 'logs' && !pauseLogs.value) {
    const data = await rpc<any[]>('logs', { since: logs.value.at(-1)?.id || 0 }); logs.value = [...logs.value, ...data].slice(-2000)
  }
  if (page.value === 'dns') {
    const data = await rpc<any[]>('dns.history', { since: dnsHistory.value.at(-1)?.id || 0 }); dnsHistory.value = [...dnsHistory.value, ...data].slice(-1000)
  }
  if (page.value === 'overview' || page.value === 'proxies') await refreshGroups()
  if (!status.value.running) { connections.value = []; rules.value = []; providers.value = {}; delayResults.value = {}; batchSummary.value = ''; return }
  if (page.value === 'connections' || page.value === 'devices') { const data = await rpc('connections'); connections.value = data.connections || [] }
  if (page.value === 'rules' && ruleTab.value === 'active') { const data = await rpc('rules'); rules.value = data.rules || [] }
  if (page.value === 'rules' && ruleTab.value === 'providers') providers.value = await rpc('providers')
}

async function refreshGroups() {
  const expected = status.value?.running ? 'core' : 'config'
  if (expected === 'config' && proxySource.value === expected && Date.now() - groupsReadAt < 15000) return
  if (proxySource.value !== expected) { proxyData.value = {}; proxySource.value = ''; previewProviders.value = []; providerPreviews.value = [] }
  const data = await rpc('groups')
  proxyData.value = data.proxies || {}; proxySource.value = data.source || 'core'
  previewProviders.value = data.providers || []; providerPreviews.value = data.providerPreviews || []; groupsReadAt = Date.now()
}

async function poll() {
  if (polling || document.hidden) return
  polling = true
  try {
    const next = await rpc<Status>('status'); status.value = next;
    if (!next.running) { batchCancelled.value = true; rules.value = []; providers.value = {}; delayResults.value = {}; batchSummary.value = '' }
    if (page.value === 'profiles' && !busy.value) { const data = await rpc<{state: State; nextUpdates?: Record<string,string>}>('state.get'); state.value = data.state; nextUpdates.value = data.nextUpdates || {} }
    connected.value = true; error.value = ''
    if (!state.value || !settings.value) await loadState()
    const now = Date.now(), up = next.uploadTotal || 0, down = next.downloadTotal || 0
    if (lastTraffic && now > lastTraffic.time && up >= lastTraffic.up && down >= lastTraffic.down) {
      const seconds = (now - lastTraffic.time) / 1000; rate.value = { up: (up - lastTraffic.up) / seconds, down: (down - lastTraffic.down) / seconds }
      samples.value = [...samples.value.slice(1), rate.value.up + rate.value.down]
    } else rate.value = { up: 0, down: 0 }
    lastTraffic = { time: now, up, down }
    if (!busy.value) await refreshPage()
  } catch (e: any) { connected.value = false; rate.value = { up: 0, down: 0 }; lastTraffic = null; error.value = e.message || String(e) }
  finally { polling = false }
}

function service() { void run(status.value?.running ? '正在停止代理' : '正在启动代理', () => actionJob(status.value?.running ? 'service.stop' : 'service.start'), status.value?.running ? '已恢复直连' : '代理已启动') }
function changeMode(mode: string) { void run('正在切换模式', () => rpc('mode.set', { mode }), `已切换${modeLabel(mode)}模式`) }

function newProfile() {
  Object.assign(formProfile, { id: '', name: '', url: '', userAgent: '', updateHours: 24 }); profileContent.value = ''; profileRevision.value = ''; profileFile.value = null; editingProfile.value = true
}
async function editProfile(profile: Profile) {
  await run('正在读取配置', async () => { const data = await rpc('profiles.read', { id: profile.id }); Object.assign(formProfile, data.profile || profile); profileRevision.value = data.revision || ''; profileContent.value = data.content; profileFile.value = null; editingProfile.value = true }, '配置已载入编辑器')
}
function importProfile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]; if (!file) return
  if (file.size > 8 * 1024 * 1024) { toast('配置不能超过 8 MiB', 'error'); return }
  profileFile.value = file; if (!formProfile.name) formProfile.name = file.name.replace(/\.ya?ml$/i, '')
  void file.text().then(text => profileContent.value = text)
}
function saveProfile() {
  void run('正在校验并保存配置', async () => {
    const text = profileContent.value.trim()
    if (text && new TextEncoder().encode(text).length > 64 * 1024) {
      await uploadFile('profile', new File([profileContent.value], 'profile.yaml'), { ...formProfile, revision: profileRevision.value }, p => progress.value = p)
    } else await actionJob('profiles.save', { ...formProfile, content: profileContent.value, revision: profileRevision.value })
    editingProfile.value = false
  }, '配置已通过检查并保存')
}
function chooseProfile(profile: Profile) { void run('正在切换配置', () => actionJob('profiles.select', { id: profile.id }), '配置已切换') }
function updateProfile(profile: Profile) { void run('正在更新订阅', () => actionJob('profiles.update', { id: profile.id }), '订阅已更新') }
function deleteProfile(profile: Profile) { if (state.value?.activeProfile === profile.id && status.value?.running) { toast('请先停止代理，再删除当前配置', 'error'); return }; ask('删除配置', `删除“${profile.name}”？`, () => void run('正在删除配置', () => rpc('profiles.delete', { id: profile.id }), '配置已删除')) }

function selectProxy(group: string, node: string) {
  void run('正在选择线路', async () => {
    await rpc('groups.select', {group,node})
    if (nodePrefs.closeConnections) {
      try { await rpc('connections.close') } catch { throw new Error('线路已切换，但旧连接关闭失败，请到连接页重试') }
    }
  }, nodePrefs.closeConnections ? '线路已切换，旧连接已关闭' : '线路已切换，已有连接继续使用原线路')
}
async function stopDuringTask() {
  if (stopPending.value) return
  stopPending.value = true; batchCancelled.value = true
  try { await actionJob('service.stop'); await loadState(); await refreshPage(); toast('代理已停止，配置处理已取消') }
  catch (e:any) { toast(e.message,'error') }
  finally { stopPending.value = false }
}

async function measureDelay(name: string) {
  validateNodePrefs()
  const baseline = historyKey(proxyData.value[name]?.history), url = nodePrefs.url
  try {
    const result = await rpc<{delay: number}>('groups.delay', { group: name, url, timeout: nodePrefs.timeout })
    if (!(result.delay > 0)) throw new Error('测速超时或无有效延迟')
    delayResults.value[name] = {delay: result.delay, baseline, url}
  } catch (e) { delayResults.value[name] = {delay: 0, baseline, url}; throw e }
}
async function testDelay(name: string) { await run('正在测试延迟', () => measureDelay(name), '延迟测试完成') }
function testGroup(group: any) {
  void run('正在批量测速', async () => {
    validateNodePrefs()
    const names = [...new Set<string>(group.all)]; let index = 0, completed = 0, failed = 0
    batchSummary.value = ''; batchActive.value = true; batchCancelled.value = false
    try {
      const workers = Math.min(8, Math.max(1, Number(nodePrefs.concurrency) || 4), names.length)
      await Promise.all(Array.from({ length: workers }, async () => {
        while (!batchCancelled.value && index < names.length) {
          const node = names[index++]
          try { await measureDelay(node) } catch { failed++ }
          completed++; progress.value = Math.round(completed / names.length * 100)
        }
      }))
      batchSummary.value = `共 ${completed} 项：成功 ${completed - failed}，失败 ${failed}` + (batchCancelled.value ? `，已取消剩余 ${names.length - completed} 项` : '')
      if (batchCancelled.value || failed) throw new Error(batchSummary.value)
    } finally { batchActive.value = false }
  }, '测速完成，全部成功')
}
function updateProviders(kind: string) {
  void run('正在更新集合', async () => {
    const entries = Object.entries(providers.value[kind]?.providers || {}).filter(([,p]:any) => p.vehicleType !== 'Compatible')
    let failed = 0
    for (const [id] of entries) {
      const key = kind+':'+id; providerResults.value[key] = '更新中'
      try { await rpc('providers.update',{id,type:kind}); providerResults.value[key] = '更新成功' }
      catch(e:any) { failed++; providerResults.value[key] = '失败：'+e.message }
    }
    providers.value = await rpc('providers')
    if (failed) throw new Error(`${entries.length - failed} 个集合更新成功，${failed} 个失败，详见各项结果`)
  }, '集合更新完成')
}
function saveSettings() {
  if (!settings.value) return
  const payload = { ...settings.value }
  for (const key of Object.keys(ruleForm) as (keyof typeof ruleForm)[]) payload[key] = lines(ruleForm[key])
  void run('正在检查并应用设置', async () => {
    const text=JSON.stringify(payload)
    await (new TextEncoder().encode(text).length > 64*1024
      ? uploadFile('settings',new File([text],'settings.json'),{},p=>progress.value=p)
      : actionJob('settings.save',payload))
    await loadState('settings')
  }, '设置已保存')
}
function addPolicy(device?: any) { policies.value.push({ id: '', name: device?.name || '', ip: device?.ip || '', mac: device?.mac || '', mode: 'rule', group: '' }) }
function saveDevices() { void run('正在应用设备规则', async () => { await actionJob('devices.save', { devices: policies.value }); await loadState('devices') }, '设备规则已生效') }
function queryDNS() { void run('正在查询 DNS', async () => dnsResult.value = await rpc('dns.query', { name: dnsName.value, type: dnsType.value }), 'DNS 查询完成') }
function checkCore() { void run('正在检查稳定内核', async () => release.value = await actionJob('core.check'), '已获取官方稳定版信息') }
function upgradeCore() { ask('更新 mihomo 内核', '新内核会先校验文件与当前配置，再短暂重启代理；启动失败会尝试回退。', () => void run('正在下载并更新内核', () => actionJob('core.upgrade'), '内核已更新')) }
function uploadKernel(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]; if (!file) return
  ask('安装离线内核', `安装 ${file.name}？请使用官方 Linux ARM64 .gz 内核。`, () => void run('正在上传并校验内核', () => uploadFile('kernel', file, {}, p => progress.value = p), '离线内核已安装'))
  input.value = ''
}
function restoreBackup(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]; if (!file) return
  ask('恢复配置备份', '恢复前会保存上一份配置备份，并停止代理。恢复完成后请检查配置，再手动启用。', () => void run('正在上传并恢复备份', () => uploadFile('backup', file, {}, p => progress.value = p), '备份已恢复，代理保持关闭'))
  input.value = ''
}
function uploadAsset(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]; if (!file) return
  const path = assetPath.value.trim() || file.name
  void run('正在上传本地资源', () => uploadFile('asset', file, { path }, p => progress.value = p), `资源已保存：${path}。保存配置或更新集合后生效。`)
  input.value = ''
}
function exportLogs() { const blob = new Blob([filteredLogs.value.map(l => `${l.time} [${l.level}] ${l.message}`).join('\n')], { type: 'text/plain;charset=utf-8' }); const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = 'cwclash.log'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 10000) }
function inspectStorage() { void run('正在核对存储挂载与空间', async () => storageInfo.value = await actionJob('storage.inspect'), '存储检查完成') }
function cleanAsd() {
  const cache = storageInfo.value?.cache; if (!cache?.tar || !cache.sha256) return
  ask('清理 ASD 检查备份', `将先在内存保存 ${bytes(cache.bytes)} 的可还原副本，再清空 /jffs/.asdbk/main_checker.sh_bk。只接受刚核验的 tar 检查备份。软件中心数据库及安全服务开关保持原样；内存副本在路由器重启后消失。`, () => void run('正在备份并清理 ASD 检查备份', async () => storageInfo.value = await actionJob('storage.cleanAsd', {bytes:cache.bytes,sha256:cache.sha256}), '检查备份已清理，空间已重新检查'))
}
function undoAsd() { ask('还原 ASD 检查备份', '将最近一次清理前的检查备份还原到原路径。如果 ASD 已产生新文件内容，则保留新内容并拒绝覆盖。', () => void run('正在还原检查备份', async () => storageInfo.value = await actionJob('storage.undoAsd'), '检查备份已还原')) }

onMounted(async () => { window.addEventListener('beforeunload', beforeUnload); window.addEventListener('hashchange', onHashChange); try { await loadState(); await refreshPage() } catch (e: any) { error.value = e.message } timer = setInterval(poll, 2000) })
onUnmounted(() => { clearInterval(timer); clearTimeout(toastTimer); window.removeEventListener('hashchange', onHashChange); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <div class="app-shell">
    <header class="plugin-header"><div class="plugin-title"><Icon name="wall" :size="27" /><div><h1>长城 Clash</h1><p>{{ model }} · mihomo {{ status?.kernel.version || '—' }}<span v-if="status?.version"> · 插件 {{ status.version }}</span></p></div></div><a class="button button-secondary" href="/Module_Softcenter.asp">返回软件中心</a></header>
    <nav class="plugin-tabs" aria-label="长城 Clash 功能"><button v-for="item in pages" :key="item.id" :class="{ active: page === item.id }" :aria-current="page === item.id ? 'page' : undefined" @click="navigate(item.id)"><Icon :name="item.icon" :size="15" /><span>{{ item.label }}</span></button></nav>
    <main class="main-content">
      <header class="page-header"><h2>{{ current.label }}</h2><span :class="['connection-pill', { offline: !connected }]"><i />{{ connected ? '管理服务已连接' : error ? '管理服务未连接' : '正在连接管理服务' }}</span></header>

      <div v-if="busy && status?.running" class="banner"><span>{{ busy }}</span><button class="button button-secondary button-small" :disabled="stopPending" @click="stopDuringTask">停止代理并取消配置处理</button></div>
      <div v-if="settingsDirty || devicesDirty" class="banner draft-banner" role="status"><span>有未保存修改（{{ [settingsDirty ? '网络 / 规则' : '', devicesDirty ? '设备' : ''].filter(Boolean).join('、') }}）。切换标签会保留草稿，关闭页面前请保存。</span><button class="text-button" :disabled="!!busy" @click="discardDrafts">放弃修改</button></div>
      <div v-if="status?.dev" class="banner"><Icon name="info" />本地开发预览：不修改本机路由或防火墙，路由器接管功能以实机验证为准。</div>
      <div v-if="error" class="banner danger"><Icon name="info" /><span>{{ error }}</span><button class="text-button" @click="run('正在重新连接', loadState)">重试</button></div>
      <div v-if="status?.error" class="banner warning"><Icon name="info" />{{ status.error }}</div>
      <div v-if="connected && status && !status.kernel.file" class="banner warning"><Icon name="info" />管理服务已安装。请到设置中导入官方 Linux ARM64 .gz 内核，之后再导入配置并启用代理。</div>
      <div v-if="busy" class="operation-bar" role="status"><span class="spinner" /><span>{{ busy }}</span><span v-if="progress">{{ progress }}%</span><div v-if="progress" class="progress-track"><div :style="{ width: progress + '%' }" /></div></div>

      <template v-if="page === 'overview'">
        <div class="health-grid"><article class="card"><span>管理服务</span><strong :class="connected ? 'positive' : 'negative'">{{ connected ? '已连接' : '未连接' }}</strong><button class="text-button" :disabled="!!busy" @click="run('正在重新连接', () => loadState())">重新连接</button></article><article class="card"><span>mihomo 内核</span><strong :class="connected && status?.running ? 'positive' : 'warning'">{{ !connected ? '状态未知' : status?.running ? '运行中' : '已停止' }}</strong><button class="text-button" @click="navigate('logs')">查看日志</button></article><article class="card"><span>DNS</span><strong>{{ connected ? status?.network.dnsMode : '状态未知' }}</strong><button class="text-button" @click="navigate('dns')">测试域名解析</button><small>模式信息不代表解析检测结果</small></article><article class="card"><span>持久存储</span><strong>{{ storageInfo ? (storageInfo.ready ? '最近检查通过' : '查看检查结果') : '尚未检查' }}</strong><button class="text-button" @click="settingsTab='system'; navigate('settings')">检查挂载与容量</button></article></div><p v-if="status?.error" class="banner danger" role="alert">{{ status.error }} · 请查看日志定位启动原因。</p>
        <section :class="['hero-card', { running: capturing }]">
          <div class="hero-copy">
            <div class="eyebrow">代理服务</div>
            <h2>{{ serviceLabel }}</h2>
            <p>{{ heroDescription }}</p>
            <div class="hero-controls">
              <button :class="['button', capturing ? 'button-light' : 'button-primary']" :disabled="!!busy || !connected" @click="service"><Icon name="power" />{{ status?.running ? '停止代理' : '启动代理' }}</button>
              <button v-if="!state?.profiles.length" class="text-button" @click="navigate('profiles'); newProfile()">导入配置 <Icon name="chevron" :size="15" /></button>
              <span v-else class="hero-profile"><Icon name="folder" :size="16" />{{ profileName }}</span>
            </div>
          </div>
        </section>
        <div class="stats-grid"><article class="stat-card"><span class="stat-icon green"><Icon name="down" /></span><label>下载速度</label><strong>{{ bytes(rate.down) }}<small>/s</small></strong><span>累计 {{ bytes(status?.downloadTotal) }}</span></article><article class="stat-card"><span class="stat-icon violet"><Icon name="up" /></span><label>上传速度</label><strong>{{ bytes(rate.up) }}<small>/s</small></strong><span>累计 {{ bytes(status?.uploadTotal) }}</span></article><article class="stat-card"><span class="stat-icon blue"><Icon name="network" /></span><label>活动连接</label><strong>{{ status?.connectionCount || 0 }}<small>个</small></strong><button class="text-button" @click="navigate('connections')">查看连接 <Icon name="chevron" :size="14" /></button></article><article class="stat-card"><span class="stat-icon amber"><Icon name="cpu" /></span><label>内核内存</label><strong>{{ status?.running ? bytes(status?.memory) : '—' }}</strong><span>{{ status?.kernel.version || '内核尚未安装' }}</span></article></div>
        <div class="overview-grid"><section class="card traffic-card"><div class="section-heading"><div><h3>流量趋势</h3><p>最近一分钟的上下行速率</p></div><span class="live-label"><i />实时</span></div><div class="traffic-chart"><svg viewBox="0 0 100 48" preserveAspectRatio="none" aria-label="实时流量趋势"><defs><linearGradient id="traffic-fill" x1="0" x2="0" y1="0" y2="1"><stop offset="0%" stop-color="var(--accent)" stop-opacity=".25"/><stop offset="100%" stop-color="var(--accent)" stop-opacity="0"/></linearGradient></defs><path :d="`M0 48 L${graphPoints.replaceAll(' ', ' L')} L100 48Z`" fill="url(#traffic-fill)"/><polyline :points="graphPoints" fill="none" stroke="var(--accent)" stroke-width=".55" vector-effect="non-scaling-stroke"/></svg><div class="chart-lines"><i/><i/><i/><i/></div></div><div class="chart-labels"><span>60 秒前</span><span>30 秒前</span><span>现在</span></div></section><section class="card"><div class="section-heading"><h3>运行模式</h3><Icon name="shield" /></div><div class="mode-picker"><button v-for="mode in ['rule','global','direct']" :key="mode" :class="{ selected: status?.mode === mode }" :disabled="!!busy" @click="changeMode(mode)">{{ modeLabel(mode) }}</button></div><p class="muted">{{ status?.mode === 'global' ? '所有代理流量使用 GLOBAL 策略组。设备直连例外仍生效。' : status?.mode === 'direct' ? '局域网恢复直接联网，内核保留供手动代理使用。' : '按配置中的规则，为不同网站与服务选择合适线路。' }}</p><div class="key-value"><span>DNS 模式</span><strong>{{ status?.network.dnsMode || '—' }}</strong></div><div class="key-value"><span>IPv6</span><strong>{{ ipv6Label(status?.network.ipv6) }}</strong></div><div class="key-value"><span>故障处理</span><strong class="positive">恢复直连</strong></div></section></div>
        <section class="card quick-config"><div class="inline-icon"><Icon name="folder" /></div><div><h3>{{ profileName }}</h3><p>{{ state?.profiles.length || 0 }} 份配置 · {{ status?.kernel.version ? `mihomo ${status.kernel.version}` : '等待内核初始化' }}</p></div><button class="button button-secondary" @click="navigate('profiles')">管理配置 <Icon name="chevron" :size="15" /></button></section>
      </template>

      <template v-else-if="page === 'profiles'">
        <div class="toolbar"><p>每份订阅保留自己的规则与代理组，切换配置时统一检查。</p><button class="button button-primary" @click="newProfile"><Icon name="plus" />添加配置</button></div>
        <div v-if="!state?.profiles.length" class="empty-state card"><span class="empty-icon"><Icon name="folder" :size="36" /></span><h3>从一份配置开始</h3><p>支持 Clash / mihomo 订阅链接或 YAML 文件。</p><button class="button button-primary" @click="newProfile">导入配置</button></div>
        <div class="profile-grid"><article v-for="profile in state?.profiles" :key="profile.id" :class="['card', 'profile-card', { selected: state?.activeProfile === profile.id }]"><div class="section-heading"><span class="file-symbol"><Icon name="folder" :size="25" /></span><span v-if="state?.activeProfile === profile.id" class="tag positive"><Icon name="check" :size="13" />当前配置</span></div><h3>{{ profile.name }}</h3><p class="muted">{{ profile.url ? '在线订阅' : '本地配置' }} · {{ profile.updateHours ? `每 ${profile.updateHours} 小时更新` : '手动更新' }}</p><p class="profile-date"><Icon name="clock" :size="14" />最近保存 {{ date(profile.updatedAt) }}</p><p v-if="profile.lastAttempt" class="field-help">最近尝试 {{ date(profile.lastAttempt) }}</p><p v-if="profile.updateError" class="negative">更新失败：{{ profile.updateError }}</p><p v-if="profile.url && profile.updateHours" class="field-help">{{ profile.updateError ? '下次重试' : '下次更新' }}：{{ nextUpdates[profile.id] ? date(nextUpdates[profile.id]) : '等待调度' }}</p><div v-if="profile.url" class="subscription-quota"><template v-for="quota in [subscriptionSummary(profile.subscriptionInfo)]" :key="profile.id"><p>已用 {{ quota.used === null ? '未提供' : bytes(quota.used) }} · 剩余 {{ quota.remaining === null ? '未提供' : bytes(quota.remaining) }}</p><progress v-if="quota.percent !== null" :value="quota.percent" max="100" aria-label="订阅流量使用比例" /><p :class="{ negative: quota.expired }">{{ quota.expired ? '已到期' : '到期时间' }}：{{ quota.expiry === null ? '未提供' : date(new Date(quota.expiry).toISOString()) }}</p></template></div><div class="profile-actions"><button class="button button-secondary" :disabled="state?.activeProfile === profile.id || !!busy" @click="chooseProfile(profile)">{{ state?.activeProfile === profile.id ? '已选择' : '使用配置' }}</button><button v-if="profile.url" class="icon-button" title="更新订阅" :disabled="!!busy" @click="updateProfile(profile)"><Icon name="refresh" :size="17" /></button><button class="button button-secondary button-small" :disabled="!!busy" @click="openVisual(profile)">编辑节点/组</button><button class="icon-button" title="编辑配置" @click="editProfile(profile)"><Icon name="edit" :size="17" /></button><button class="icon-button" title="导出配置" @click="run('正在导出配置', () => downloadFile('profile', profile.id))"><Icon name="download" :size="17" /></button><button class="icon-button destructive" title="删除配置" @click="deleteProfile(profile)"><Icon name="trash" :size="17" /></button></div></article></div>
      </template>

      <template v-else-if="page === 'proxies'">
        <div class="button-row"><button class="button button-secondary" :disabled="!!busy || !state?.activeProfile" @click="openVisual(state?.profiles.find(p=>p.id===state?.activeProfile))">编辑当前配置节点/组</button></div><details class="card"><summary>测速与切换设置</summary><div class="form-grid"><label>测速地址<input v-model="nodePrefs.url" :disabled="!!busy" type="url" /></label><label>超时（毫秒）<input v-model.number="nodePrefs.timeout" :disabled="!!busy" type="number" min="1000" max="15000" /></label><label>测速并发<input v-model.number="nodePrefs.concurrency" :disabled="!!busy" type="number" min="1" max="8" /></label><label class="check-setting"><input v-model="nodePrefs.closeConnections" :disabled="!!busy" type="checkbox" /><span>切换线路后关闭全部旧连接<small>现有下载、视频等连接会重新建立，默认关闭。</small></span></label></div><button class="button button-secondary button-small" :disabled="!!busy" @click="saveNodePrefs">保存操作偏好</button><p class="field-help">保存在当前浏览器。测量延迟，不是下载带宽；更换测试地址后重新测速。内核历史没有测试地址标记时，不用于自定义地址结果。</p></details>
        <div v-if="batchActive" class="banner"><span>{{ batchCancelled ? '正在结束已发出的测速请求…' : '批量测速进行中' }}</span><button class="button button-secondary button-small" :disabled="batchCancelled" @click="batchCancelled=true">取消剩余测速</button><small>已发出的请求等待完成或超时，不再发送后续节点。</small></div>

        <div class="toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="搜索代理组或节点" aria-label="搜索代理" /></div><select v-model="nodeSort" aria-label="节点排序"><option value="default">订阅顺序</option><option value="delay">延迟从低到高</option><option value="name">按名称排序</option></select><select v-model="nodeFilter" aria-label="节点筛选"><option value="all">全部节点</option><option value="favorites">仅收藏</option><option value="available">隐藏测速超时</option></select><span class="muted">收藏保存在当前浏览器</span></div>
        <section class="card"><div class="section-heading"><div><h3>{{ canUseNodes ? '内核实时节点' : '配置预览' }}</h3><p>{{ canUseNodes ? '节点选择、代理组与延迟测试；测速结果来自 mihomo。' : '服务停止时可浏览已保存配置（含 YAML 覆写）。测速和切换线路需要内核运行。' }}</p></div><button class="button button-secondary button-small" :disabled="!!busy" @click="groupsReadAt = 0; run('正在读取节点', refreshGroups)">刷新节点</button></div><p v-if="proxySource === 'config'" class="field-help">这里只展示配置中明确列出的成员；集合加载、筛选和自动选择的最终结果以内核运行为准，不代表当前生效线路。</p><p v-if="previewProviders.length" class="field-help">代理集合：{{ previewProviders.join('、') }}。下方缓存仅供预览，最终成员以内核运行为准。</p><div class="button-row"><button class="button button-secondary button-small" @click="ruleTab = 'custom'; navigate('rules')">域名 / IP 分流</button><button class="button button-secondary button-small" @click="navigate('devices')">按设备分流</button></div></section>
        <p v-if="batchSummary" class="banner" role="status">{{ batchSummary }}</p>
        <section v-for="provider in providerPreviews" :key="provider.name" class="card"><div class="section-heading"><div><h3>{{ provider.name }}</h3><p>{{ provider.source }} · {{ provider.nodes.length }} 个节点<span v-if="provider.updatedAt"> · 文件时间 {{ date(provider.updatedAt) }}</span></p></div></div><div class="node-grid"><div v-for="(node,i) in provider.nodes.filter((n:any) => !search || n.name.toLowerCase().includes(search.toLowerCase()))" :key="i" class="node-card"><span class="node-copy"><strong>{{ node.name }}</strong><small>{{ node.type }}{{ node.udp ? ' · UDP' : '' }}</small></span></div></div><p v-if="!provider.nodes.length" class="field-help">暂无可预览数据，内核运行并加载集合后可查看。旧版本缓存首次升级后需重新加载。</p></section>
        <div v-if="!displayGroups.length && !providerPreviews.length" class="empty-state card"><h3>{{ state?.activeProfile ? '配置中暂无可预览的节点' : '尚未选择配置' }}</h3><p>可前往配置页导入或选择配置；集合中的节点需内核加载。</p><button class="button button-secondary" @click="navigate('profiles')">管理配置</button></div>
        <section v-for="group in displayGroups" v-show="!search || group.name.toLowerCase().includes(search.toLowerCase()) || group.all.some((n:string) => n.toLowerCase().includes(search.toLowerCase()))" :key="`${group.catalog ? 'catalog' : 'group'}:${group.name}`" class="card proxy-group"><div class="section-heading"><button class="group-title" @click="expandedGroups[group.name] = expandedGroups[group.name] === false"><Icon name="globe" /><span><h3>{{ group.name }}</h3><small>{{ group.type }} · {{ canUseNodes ? (group.now || '自动选择') : '配置预览' }}</small></span></button><button class="button button-secondary button-small" :disabled="!!busy || !canUseNodes" title="内核运行时可测速" @click="testGroup(group)"><Icon name="bolt" :size="15" />测速</button></div><p v-if="!visibleNodes(group).length" class="field-help">没有匹配的节点，请调整搜索或筛选。</p><div v-if="expandedGroups[group.name] !== false" class="node-grid"><div v-for="node in visibleNodes(group)" :key="node" :class="['node-card', { selected: canUseNodes && group.now === node }]"><button class="favorite-button" :aria-label="(isFavorite(group.name,node) ? '取消收藏 ' : '收藏 ') + node" :aria-pressed="isFavorite(group.name,node)" @click="toggleFavorite(group.name,node)">{{ isFavorite(group.name,node) ? '★' : '☆' }}</button><button class="node-select" :disabled="!!busy || !canUseNodes || group.type !== 'Selector'" @click="selectProxy(group.name,node)"><span class="node-radio"><Icon v-if="canUseNodes && group.now === node" name="check" :size="12" /></span><span class="node-copy"><strong>{{ node }}</strong><small>{{ proxyData[node]?.type || ({ DIRECT: '直连', REJECT: '拒绝', 'REJECT-DROP': '丢弃' } as Record<string, string>)[node] || '代理组' }}{{ proxyData[node]?.udp ? ' · UDP' : '' }}</small></span></button><button :class="['node-delay', { good: delay(node)! > 0 && delay(node)! < 300, poor: delay(node) === 0 || delay(node)! >= 600 }]" :disabled="!!busy || !canUseNodes" :title="canUseNodes ? '测试延迟' : '内核运行后可测速'" @click="testDelay(node)">{{ !canUseNodes ? '未测速' : delay(node) === null ? '测速' : delay(node) === 0 ? '失败' : `${delay(node)} ms` }}</button></div></div></section>
      </template>

      <template v-else-if="page === 'rules'">
        <div class="tabs"><button v-for="tab in [['active','生效规则'],['custom','自定义分流'],['providers','规则与代理集合']]" :key="tab[0]" :class="{ active: ruleTab === tab[0] }" @click="ruleTab = tab[0]; refreshPage().catch(e => toast(e.message,'error'))">{{ tab[1] }}</button></div>
        <template v-if="ruleTab === 'active'"><div class="toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="搜索规则或目标策略" /></div><span class="muted">{{ filteredRules.length }} 条规则</span></div><div class="card table-wrap"><table><thead><tr><th>类型</th><th>匹配内容</th><th>目标策略</th></tr></thead><tbody><tr v-for="(rule,i) in filteredRules.slice(0,500)" :key="i"><td><span class="tag">{{ rule.type }}</span></td><td class="mono">{{ rule.payload || '全部流量' }}</td><td>{{ rule.proxy }}</td></tr></tbody></table><p v-if="!rules.length" class="table-empty">代理运行后显示生效规则。</p><p v-if="filteredRules.length > 500" class="table-empty">显示前 500 条，请使用搜索缩小范围。</p></div></template>
        <section v-if="ruleTab === 'custom'" class="card"><div class="section-heading"><div><h3>自定义分流</h3><p>设备规则优先，其次是直连列表、代理列表、自定义规则和订阅规则。</p></div><button class="button button-primary" :disabled="!!busy" @click="saveSettings">保存并应用</button></div><div class="form-grid"><label>直连域名<textarea v-model="ruleForm.directDomains" rows="7" placeholder="每行一个域名，包含其子域名" /></label><label>代理域名<textarea v-model="ruleForm.proxyDomains" rows="7" placeholder="每行一个域名" /></label><label>直连 IP / CIDR<textarea v-model="ruleForm.directCidrs" rows="5" placeholder="例如 192.0.2.0/24" /></label><label>代理 IP / CIDR<textarea v-model="ruleForm.proxyCidrs" rows="5" placeholder="每行一条 CIDR" /></label></div><label>自定义 mihomo 规则<textarea v-model="ruleForm.customRules" rows="6" class="code-editor" placeholder="DOMAIN-SUFFIX,example.com,策略组名称" /></label><p class="field-help">代理列表使用设置中的默认策略组；配置更新会保留这些自定义内容。</p></section>
        <section v-if="ruleTab === 'providers' && settings" class="card"><div class="section-heading"><div><h3>自动更新计划</h3><p>在线集合与 GEO 资源由 mihomo 在运行期间更新。</p></div><button class="button button-primary" :disabled="!!busy" @click="saveSettings">保存更新计划</button></div><div class="form-grid"><label>在线集合周期（小时）<input v-model.number="settings.providerUpdateHours" type="number" min="0" max="720" /></label><label>GEO 自动更新<select v-model="settings.geoAutoUpdate" aria-label="GEO 自动更新"><option :value="null">沿用配置</option><option :value="true">开启</option><option :value="false">关闭</option></select></label><label>GEO 周期（小时）<input v-model.number="settings.geoUpdateHours" type="number" min="0" max="720" /></label></div><p class="field-help">0 表示沿用 YAML；填写 1–720 覆盖周期。集合周期仅覆盖 HTTP 规则/代理集合，不改变节点健康检查频率。保存会一并应用设置草稿，运行中会短暂重启代理。GEO 周期本身不启用自动更新；不会修改原始订阅文件。</p></section>
        <div v-if="ruleTab === 'providers'" class="provider-grid"><section v-for="kind in ['proxies','rules']" :key="kind" class="card"><div class="section-heading"><h3>{{ kind === 'proxies' ? '代理集合' : '规则集合' }}</h3><button class="button button-secondary button-small" :disabled="!!busy || !status?.running" @click="updateProviders(kind)">更新全部</button></div><div v-for="(provider,name) in (providers[kind]?.providers || {})" :key="name" class="provider-row"><div><strong>{{ name }}</strong><small>{{ provider.vehicleType || provider.type }} · {{ date(provider.updatedAt) }}</small><small v-if="providerResults[kind+':'+name]">{{ providerResults[kind+':'+name] }}</small></div><button class="icon-button" title="更新集合" :disabled="!!busy || !status?.running" @click="run('正在更新集合', () => rpc('providers.update',{ id:name,type:kind }))"><Icon name="refresh" :size="17" /></button></div><p v-if="!Object.keys(providers[kind]?.providers || {}).length" class="muted">{{ status?.running ? '当前配置没有可显示的集合。' : '代理已停止，启动后查看实时集合状态。' }}</p></section></div>
      </template>

      <template v-else-if="page === 'connections'">
        <div class="toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="搜索域名、IP、来源设备" /></div><button class="button button-secondary" :disabled="!!busy || !status?.running" @click="ask('关闭全部连接','现有连接将被断开，应用可能自动重新连接。',() => run('正在关闭连接',()=>rpc('connections.close')))">关闭全部</button></div><div class="card table-wrap"><table><thead><tr><th>目标</th><th>来源</th><th>策略链</th><th>流量 ↓ / ↑</th><th /></tr></thead><tbody><tr v-for="connection in filteredConnections.slice(0,300)" :key="connection.id"><td><strong>{{ connection.metadata?.host || connection.metadata?.destinationIP }}</strong><small>{{ connection.metadata?.network?.toUpperCase() }} · {{ connection.metadata?.destinationIP }}:{{ connection.metadata?.destinationPort }}</small></td><td class="mono">{{ state?.devices.find(d => d.ip === connection.metadata?.sourceIP)?.name || connection.metadata?.sourceIP }}<small>{{ connection.rule }}</small></td><td>{{ connection.chains?.join(' → ') }}</td><td class="mono nowrap">{{ bytes(connection.download) }} / {{ bytes(connection.upload) }}</td><td><button class="icon-button" title="关闭连接" @click="run('正在关闭连接',()=>rpc('connections.close',{id:connection.id}))"><Icon name="close" :size="15" /></button></td></tr></tbody></table><p v-if="!filteredConnections.length" class="table-empty">{{ status?.running ? '暂无匹配的连接。' : '代理尚未启动。' }}</p></div>
      </template>

      <template v-else-if="page === 'dns'">
        <section class="card"><div class="section-heading"><div><h3>解析诊断</h3><p>使用当前 mihomo DNS 配置查询域名。</p></div><div class="button-row"><span class="tag">{{ status?.network.dnsMode }}</span><button class="button button-secondary button-small" :disabled="!!busy || !status?.running" @click="ask('清空 DNS 缓存','同时清空 mihomo DNS 与 Fake-IP 映射，现有 Fake-IP 连接可能需要重新建立。',()=>run('正在清空缓存',()=>rpc('cache.clear'),'DNS 与 Fake-IP 缓存已清空'))">清空缓存</button></div></div><form class="dns-query" @submit.prevent="queryDNS"><input v-model="dnsName" placeholder="输入域名，例如 example.com" required aria-label="查询域名" /><select v-model="dnsType" aria-label="记录类型"><option v-for="type in ['A','AAAA','CNAME','MX','TXT','NS','HTTPS']" :key="type">{{ type }}</option></select><button class="button button-primary" :disabled="!!busy || !status?.running">查询</button></form><pre v-if="dnsResult" class="json-result">{{ JSON.stringify(dnsResult,null,2) }}</pre></section><div class="toolbar"><h3>局域网查询记录</h3><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="搜索域名或设备 IP" /></div></div><div class="card table-wrap"><table><thead><tr><th>时间</th><th>域名</th><th>来源</th><th>类型</th><th>结果 / 耗时</th></tr></thead><tbody><tr v-for="query in [...filteredDNS].reverse().slice(0,300)" :key="query.id"><td class="mono nowrap">{{ time(query.time) }}</td><td>{{ query.name }}</td><td class="mono">{{ query.client }}</td><td>{{ ({1:'A',28:'AAAA',5:'CNAME',15:'MX',16:'TXT'} as Record<number,string>)[query.type] || query.type }}</td><td><span :class="query.rcode === 0 ? 'positive' : 'negative'">{{ query.rcode === 0 ? '成功' : `RCODE ${query.rcode}` }}</span> · {{ query.durationMs }} ms</td></tr></tbody></table><p v-if="!dnsHistory.length" class="table-empty">启用透明代理后，将显示经过本插件的 DNS 查询。</p></div>
      </template>

      <template v-else-if="page === 'devices'">
        <section class="card"><div class="section-heading"><div><h3>设备规则</h3><p>按 IP 地址生效，建议在固件中为设备固定 DHCP 地址。</p></div><div class="button-row"><button class="button button-secondary" @click="addPolicy()"><Icon name="plus" :size="16" />添加</button><button class="button button-primary" :disabled="!!busy" @click="saveDevices">保存并应用</button></div></div><div v-for="(policy,i) in policies" :key="i" class="policy-row"><label>名称<input v-model="policy.name" placeholder="设备名称" /></label><label>IP 地址<input v-model="policy.ip" placeholder="192.168.50.x" /></label><label>模式<select v-model="policy.mode"><option value="rule">跟随规则</option><option value="direct">直接连接</option><option value="proxy">指定代理组</option></select></label><label v-if="policy.mode === 'proxy'">代理组<input v-model="policy.group" list="group-names" placeholder="留空使用默认策略组" /></label><button class="icon-button destructive" title="移除此设备规则" @click="policies.splice(i,1)"><Icon name="trash" :size="17" /></button></div><p v-if="!policies.length" class="muted">尚未添加例外规则，局域网设备默认跟随当前配置。</p></section><section class="card"><div class="section-heading"><h3>已发现设备</h3><button class="icon-button" title="刷新设备" @click="refreshPage().catch(e=>toast(e.message,'error'))"><Icon name="refresh" /></button></div><div v-for="device in devices" :key="device.ip" class="discovered-device"><span class="device-icon"><Icon name="network" /></span><div><strong>{{ device.name || '局域网设备' }}</strong><small>{{ device.ip }} · {{ device.mac }}</small></div><small v-if="connected && status?.running" class="device-traffic">{{ connections.filter(c => c.metadata?.sourceIP === device.ip).length }} 条活动连接 · ↓ {{ bytes(connections.filter(c => c.metadata?.sourceIP === device.ip).reduce((n,c) => n + (c.download || 0), 0)) }}<br>{{ [...new Set(connections.filter(c => c.metadata?.sourceIP === device.ip).flatMap(c => c.chains || []))].join(' / ') || '暂无活动策略' }}</small><button class="button button-secondary button-small" @click="addPolicy(device)">添加规则</button></div><p v-if="!devices.length" class="muted">暂未从 DHCP 租约或邻居表发现设备。</p></section>
      </template>

      <template v-else-if="page === 'logs'">
        <div class="toolbar"><div class="search-input"><Icon name="search" :size="17" /><input v-model="search" placeholder="搜索日志" /></div><div class="button-row"><select v-model="logLevel"><option value="all">全部级别</option><option v-for="level in ['info','warning','error','core']" :key="level">{{ level }}</option></select><button class="button button-secondary" @click="pauseLogs=!pauseLogs">{{ pauseLogs ? '继续更新' : '暂停更新' }}</button><button class="icon-button" title="导出日志" @click="exportLogs"><Icon name="download" /></button></div></div><section class="card log-view" role="log"><div v-for="entry in [...filteredLogs].reverse()" :key="entry.id" class="log-line"><time>{{ time(entry.time) }}</time><span :class="['log-level',entry.level]">{{ entry.level }}</span><span>{{ entry.message }}</span></div><p v-if="!logs.length" class="table-empty">管理服务运行后将显示日志。</p></section>
      </template>

      <template v-else-if="page === 'settings'">
        <div class="tabs" aria-label="设置分类"><button v-for="tab in [['network','常用设置'],['advanced','高级配置'],['system','内核与存储'],['data','备份与诊断']]" :key="tab[0]" :class="{active: settingsTab === tab[0]}" @click="settingsTab=tab[0]">{{ tab[1] }}</button></div>
        <p class="field-help">{{ status?.storageMode === "usb-jffs" ? "配置、内核、在线规则和 GeoIP 缓存保存到 U 盘，重启后继续使用；运行程序驻留内存。" : "在线规则与 GeoIP 缓存使用内存，重启后按需重新下载；配置和手动线路选择会保留。" }}</p>
        <section v-show="settingsTab === 'system'" class="card"><div class="section-heading"><div><h3>{{ status?.storageMode === 'usb-jffs' ? 'U 盘存储' : 'JFFS 存储' }}</h3><p>{{ status?.storageMode === 'usb-jffs' ? '核对实际挂载位置、容量与 U 盘状态。' : '检查内置存储与 ASD 检查备份。' }}</p></div><button class="button button-secondary" :disabled="!!busy || !connected" @click="inspectStorage">检查存储</button></div><template v-if="storageInfo?.supported"><div class="key-value"><span>已用 / 总量</span><strong>{{ bytes((storageInfo.usedKiB || 0) * 1024) }} / {{ bytes((storageInfo.totalKiB || 0) * 1024) }}</strong></div><div class="key-value"><span>剩余空间</span><strong>{{ bytes((storageInfo.freeKiB || 0) * 1024) }}</strong></div><template v-if="storageInfo.mode === 'usb-jffs'"><div class="key-value"><span>U 盘挂载</span><strong>{{ storageInfo.ready ? 'EXT4 · 已核验' : '挂载不可用' }}</strong></div><p class="field-help mono">{{ storageInfo.root }}<br>{{ storageInfo.dataDir }}</p><p class="field-help">内核直接保存原始文件，无需 XZ 解压。U 盘挂载发生变化时会停止新的持久写入并撤销代理接管；重新挂载后请重启插件管理服务。</p></template><template v-else><div class="key-value"><span>ASD 检查备份</span><strong>{{ storageInfo.cache?.exists ? bytes(storageInfo.cache.bytes) : '不存在' }}</strong></div><p class="field-help mono">{{ storageInfo.asdPath }}</p><div class="button-row"><button class="button button-secondary" :disabled="!!busy || !storageInfo.cache?.tar || !storageInfo.cache?.sha256" @click="cleanAsd">备份并清理检查备份</button><button v-if="storageInfo.undoAvailable" class="text-button" :disabled="!!busy" @click="undoAsd">撤销最近一次清理</button></div><p class="field-help">仅清理经过核验的 ASD tar 检查备份；内存撤销副本保留到路由器重启，软件中心数据库不会被清理。</p></template></template><p v-if="storageInfo?.note" class="field-help">{{ storageInfo.note }}</p></section>
        <p v-if="!settings" class="field-help">正在等待管理服务返回设置。连接恢复后，存储检查按钮会启用。</p>
        <template v-if="settings">
        <section v-show="settingsTab === 'system'" class="card"><div class="section-heading"><div><h3>mihomo 内核</h3><p>使用官方稳定版本，更新前检查文件与配置。</p></div><span class="tag positive">{{ state?.kernel.version || '尚未安装' }}</span></div><div class="kernel-info"><span class="kernel-logo"><Icon name="bolt" :size="28" /></span><div><strong>mihomo · Linux ARM64</strong><small v-if="state?.kernel.sha256" class="mono">SHA-256 {{ state.kernel.sha256.slice(0,20) }}…</small></div><button class="button button-secondary" :disabled="!!busy" @click="checkCore"><Icon name="refresh" :size="16" />检查更新</button></div><div v-if="release" class="release-box"><div><strong>最新稳定版 {{ release.version }}</strong><small>{{ date(release.published) }} · {{ bytes(release.size) }}</small></div><button class="button button-primary" :disabled="!!busy || release.version === state?.kernel.version" @click="upgradeCore">{{ release.version === state?.kernel.version ? '已是最新' : '更新内核' }}</button></div><div class="button-row"><label class="button button-secondary upload-label"><Icon name="upload" :size="16" />上传离线内核<input type="file" accept=".gz" @change="uploadKernel" /></label><button v-if="state?.kernel.previous" class="button button-secondary" :disabled="!!busy" @click="run('正在回退内核',()=>actionJob('core.rollback'),'已回退到上一内核')">回退至 {{ state.kernel.previous.version }}</button><a class="text-button" href="https://github.com/MetaCubeX/mihomo/releases" target="_blank" rel="noreferrer">官方发布 <Icon name="external" :size="14" /></a></div></section>
        <section v-show="settingsTab === 'network'" class="card"><div class="section-heading"><div><h3>网络设置</h3><p>修改会先检查配置，再短暂重启代理。</p></div><button class="button button-primary" :disabled="!!busy" @click="saveSettings">保存并应用</button></div><div class="form-grid"><label>LAN 网桥<input v-model="settings.lanInterface" placeholder="br0" /></label><label>默认代理组<input v-model="settings.defaultGroup" list="group-names" placeholder="留空使用配置中的首个策略组" /></label><label>DNS 模式<select v-model="settings.dnsMode"><option value="redir-host">Redir-Host · 便于故障恢复直连</option><option value="fake-ip">Fake-IP</option></select></label><label>IPv6 处理<select v-model="settings.ipv6"><option value="auto">自动 · 支持时接管，否则防绕过</option><option value="proxy">要求 IPv6 透明代理</option><option value="block">受控设备阻止 IPv6 外网直出</option></select></label><label>日志级别<select v-model="settings.logLevel"><option v-for="level in ['silent','error','warning','info','debug']" :key="level">{{ level }}</option></select></label><label class="check-setting"><input v-model="settings.blockQuic" type="checkbox" /><span>屏蔽代理设备的 QUIC<small>阻止 UDP 443，部分应用会改用 TCP。</small></span></label></div><button class="text-button" @click="showAdvanced=!showAdvanced">{{ showAdvanced ? '收起' : '展开' }}高级端口设置 <Icon name="chevron" :size="14" /></button><div v-if="showAdvanced" class="form-grid advanced-grid"><label>HTTP / SOCKS 混合端口<input v-model.number="settings.mixedPort" type="number" min="1024" max="65535" /></label><label>TCP 透明代理端口<input v-model.number="settings.redirPort" type="number" min="1024" max="65535" /></label><label>TPROXY 端口<input v-model.number="settings.tproxyPort" type="number" min="1024" max="65535" /></label><label>DNS 接管端口<input v-model.number="settings.dnsPort" type="number" min="1024" max="65535" /></label><label>本机控制端口<input v-model.number="settings.controllerPort" type="number" min="1024" max="65535" /></label></div><p class="field-help">Fake-IP 模式下，故障后的个别客户端可能需要等待 DNS 缓存过期。</p></section>
        <section v-show="settingsTab === 'advanced'" class="card"><div class="section-heading"><div><h3>配置覆写</h3><p>以 YAML 合并到订阅配置中，适合自定义 DNS、策略组与其他内核参数。</p></div><button class="button button-secondary" :disabled="!!busy" @click="saveSettings">保存覆写</button></div><textarea v-model="settings.overrideYaml" class="code-editor" rows="9" :placeholder="'例如：\ndns:\n  nameserver:\n    - https://dns.alidns.com/dns-query'" /><p class="field-help">路由器接管端口、控制接口和启动方式由插件统一管理。</p></section>
        <section v-show="settingsTab === 'advanced'" class="card"><div class="section-heading"><div><h3>本地集合文件</h3><p>上传文件节点集合或规则集合，供当前配置引用。单文件最大 8 MiB。</p></div><Icon name="folder" /></div><label>资源相对路径<input v-model="assetPath" placeholder="例如 providers/local.yaml，留空使用文件名" /></label><div class="button-row"><label class="button button-secondary upload-label"><Icon name="upload" :size="16" />上传资源<input type="file" :disabled="!!busy || !connected" @change="uploadAsset" /></label></div><p class="field-help">配置中 type: file 的 path 使用相同相对路径。上传后保存配置或更新集合；同名文件会被替换。资源会包含在配置备份中。</p></section>
        <section v-show="settingsTab === 'data'" class="card"><div class="section-heading"><div><h3>备份与恢复</h3><p>保存订阅、原始配置、规则、设备设置和本地集合文件。内核及在线规则缓存单独管理。</p></div><Icon name="folder" /></div><div class="button-row"><button class="button button-secondary" :disabled="!!busy" @click="run('正在生成备份',()=>downloadFile('backup'),'备份已下载')"><Icon name="download" :size="16" />下载配置备份</button><label class="button button-secondary upload-label"><Icon name="upload" :size="16" />恢复备份<input type="file" accept=".zip" @change="restoreBackup" /></label><button class="text-button" :disabled="!!busy" @click="run('正在获取环境信息',async()=>probe=await rpc('probe'),'环境信息已获取')">环境诊断</button></div><p class="field-help">备份包含订阅地址和节点凭据，请妥善保存。</p><pre v-if="probe" class="json-result">{{ JSON.stringify(probe,null,2) }}</pre></section>
        <div class="about-line"><span>长城 Clash {{ status?.version }} · GPL-3.0-or-later</span><span>为 ASUSWRT / Merlin 软件中心构建</span></div>
        </template>
      </template>

      <datalist id="group-names"><option v-for="name in groupNames" :key="name" :value="name" /></datalist>
      <footer class="page-footer"><span><i :class="{ running: capturing }" />{{ serviceLabel }}</span><span>mihomo {{ status?.kernel.version || '—' }} · 长城 Clash</span></footer>
    </main>

    <Transition name="toast"><div v-if="notice" :class="['toast-message',notice.kind]" role="alert"><Icon :name="notice.kind === 'error' ? 'info' : 'check'" /><span>{{ notice.text }}</span><button class="icon-button" aria-label="关闭提示" @click="notice=null"><Icon name="close" :size="16" /></button></div></Transition>

    <ProxyEditor v-if="visualEditor" :content="visualEditor.content" :profile="visualEditor.profile" :busy="!!busy" :protected-names="visualProtected" :has-override="!!state?.settings.overrideYaml" @close="visualEditor=null" @save="saveVisual" />
    <div v-if="editingProfile" class="modal-backdrop" @click.self="!busy && (editingProfile=false)"><section class="modal profile-modal" role="dialog" aria-modal="true" aria-labelledby="profile-title"><div class="section-heading"><div><h2 id="profile-title">{{ formProfile.id ? '编辑配置' : '添加配置' }}</h2><p>导入订阅链接、YAML 文件或直接编辑配置。</p></div><button class="icon-button" aria-label="关闭编辑器" :disabled="!!busy" @click="editingProfile=false"><Icon name="close" /></button></div><div class="form-grid"><label>配置名称<input v-model="formProfile.name" placeholder="例如：我的订阅" maxlength="100" /></label><label>自动更新<select v-model.number="formProfile.updateHours"><option :value="0">手动更新</option><option :value="6">每 6 小时</option><option :value="12">每 12 小时</option><option :value="24">每天</option><option :value="168">每周</option></select></label></div><label>订阅地址<input v-model="formProfile.url" type="url" placeholder="https://…" autocomplete="off" spellcheck="false" /></label><label>订阅 User-Agent<input v-model="formProfile.userAgent" placeholder="默认 clash.meta / cwclash" /></label><div class="editor-heading"><label>配置内容 <small>{{ profileContent ? `${bytes(new TextEncoder().encode(profileContent).length)}` : '为空时从订阅地址下载' }}</small></label><label class="button button-secondary button-small upload-label"><Icon name="upload" :size="14" />选择 YAML 文件<input type="file" accept=".yaml,.yml,.txt" @change="importProfile" /></label></div><textarea v-model="profileContent" class="code-editor" rows="12" placeholder="粘贴完整 Clash / mihomo YAML 配置，也可以留空以下载订阅。" spellcheck="false" /><div class="modal-actions"><button class="button button-secondary" :disabled="!!busy" @click="editingProfile=false">取消</button><button class="button button-primary" :disabled="!!busy || !formProfile.name || (!profileContent.trim() && !formProfile.url)" @click="saveProfile"><span v-if="busy" class="spinner" />{{ busy ? '正在校验…' : '校验并保存' }}</button></div></section></div>

    <div v-if="confirmation" class="modal-backdrop" @click.self="confirmation=null"><section class="modal confirm-modal" role="dialog" aria-modal="true" aria-labelledby="confirm-title"><span class="confirm-icon"><Icon name="info" :size="30" /></span><h2 id="confirm-title">{{ confirmation.title }}</h2><p>{{ confirmation.text }}</p><div class="modal-actions"><button class="button button-secondary" @click="confirmation=null">取消</button><button class="button button-primary" @click="confirmation.confirm()">确认</button></div></section></div>
  </div>
</template>
