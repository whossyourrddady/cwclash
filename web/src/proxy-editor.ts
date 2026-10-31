import {parseDocument, isMap, isSeq, isScalar, isAlias, visit} from 'yaml'

export type Entry = Record<string, any>
export type Kind = 'proxies' | 'proxy-groups'
export const builtins = ['DIRECT','REJECT','REJECT-DROP','PASS','COMPATIBLE','GLOBAL']
export const proxyTypes = ['ss','ssr','vmess','vless','trojan','hysteria','hysteria2','tuic','socks5','http','snell','wireguard','ssh','mieru','anytls','shadowquic','gost-relay','sudoku','masque','trusttunnel','openvpn','tailscale','zerotier','easytier','rematch','direct','reject','dns']
export const groupTypes = ['select','url-test','fallback','load-balance']
export type Field = {key:string; label:string; type?:string; choices?:string[]}
const f = (key:string,label:string,type='text',choices?:string[]):Field => ({key,label,type,choices})
export function proxyFields(type:string):Field[] {
  const specific:Record<string,Field[]> = {
    ss:[f('cipher','加密方式','text',['aes-128-gcm','aes-256-gcm','chacha20-ietf-poly1305','2022-blake3-aes-128-gcm','2022-blake3-aes-256-gcm','2022-blake3-chacha20-poly1305']),f('password','密码','password')],
    ssr:[f('cipher','加密方式'),f('password','密码','password'),f('protocol','协议'),f('obfs','混淆')],
    vmess:[f('uuid','UUID'),f('alterId','Alter ID','number'),f('cipher','加密方式')],
    vless:[f('uuid','UUID'),f('flow','Flow')], trojan:[f('password','密码','password')], anytls:[f('password','密码','password')],
    hysteria:[f('auth-str','认证字符串','password'),f('up','上行速率'),f('down','下行速率')],
    hysteria2:[f('password','密码','password'),f('obfs','混淆'),f('obfs-password','混淆密码','password')],
    tuic:[f('uuid','UUID'),f('password','密码','password'),f('congestion-controller','拥塞控制','select',['cubic','bbr','new_reno'])],
    socks5:[f('username','用户名'),f('password','密码','password')], http:[f('username','用户名'),f('password','密码','password')],
    snell:[f('psk','PSK','password'),f('version','协议版本','number')],
    wireguard:[f('ip','本机 IPv4'),f('ipv6','本机 IPv6'),f('private-key','私钥','password'),f('public-key','公钥'),f('pre-shared-key','预共享密钥','password'),f('mtu','MTU','number')],
    ssh:[f('username','用户名'),f('password','密码','password'),f('private-key','私钥或路径','password')],
    shadowquic:[f('username','用户名'),f('password','密码','password')], 'gost-relay':[f('username','用户名'),f('password','密码','password')], trusttunnel:[f('username','用户名'),f('password','密码','password')],
    sudoku:[f('key','密钥','password')], masque:[f('private-key','私钥','password'),f('public-key','公钥')], openvpn:[f('username','用户名'),f('password','密码','password'),f('ca','CA 证书或路径')], zerotier:[f('network','网络 ID')],
    mieru:[f('username','用户名'),f('password','密码','password'),f('transport','传输','select',['TCP','UDP'])]
  }
  return [...(!['direct','reject','dns','rematch','tailscale','zerotier','easytier'].includes(type)?[f('server','服务器'),f('port','端口','number')]:[]),...(specific[type]||[])]
}
export const transportFields = [f('network','传输方式','select',['tcp','ws','grpc','h2','http']),f('ws-opts.path','WebSocket 路径'),f('ws-opts.headers.Host','WebSocket Host'),f('grpc-opts.grpc-service-name','gRPC 服务名'),f('flow','Flow'),f('servername','TLS Server Name'),f('sni','SNI'),f('client-fingerprint','客户端指纹'),f('reality-opts.public-key','Reality 公钥'),f('reality-opts.short-id','Reality Short ID'),f('dialer-proxy','前置代理 / 代理组'),f('plugin','插件'),f('plugin-opts.mode','插件模式'),f('plugin-opts.host','插件 Host'),f('plugin-opts.path','插件路径'),f('fingerprint','证书指纹')]
export const groupFields = [f('url','测速地址'),f('interval','检测周期（秒）','number'),f('timeout','超时（毫秒）','number'),f('tolerance','切换容差（毫秒）','number'),f('max-failed-times','最大失败次数','number'),f('filter','包含正则'),f('exclude-filter','排除正则'),f('exclude-type','排除协议'),f('expected-status','预期 HTTP 状态'),f('strategy','负载均衡策略','select',['consistent-hashing','round-robin','sticky-sessions']),f('hash-key','负载均衡 Hash Key'),f('default-selected','默认成员'),f('empty-fallback','空组后备'),f('icon','图标 URL')]
export function getField(value:Entry,key:string):any { return key.split('.').reduce((v,k)=>v?.[k],value) ?? '' }
export function setField(value:Entry,key:string,next:any) {
  const parts=key.split('.');let at=value
  for(const k of parts.slice(0,-1)) { if(!at[k]||typeof at[k]!=='object'||Array.isArray(at[k])) at[k]={};at=at[k] }
  const last=parts.at(-1)!;if(next===''||next===undefined) delete at[last];else at[last]=next
}
function docOf(source:string) {
  const doc=parseDocument(source,{merge:true,uniqueKeys:true,prettyErrors:false})
  if(doc.errors.length||!isMap(doc.contents)) throw new Error('配置 YAML 无效，无法可视化编辑；请先在 YAML 编辑器修正')
  doc.toJS({maxAliasCount:100}) // Refuse recursive/excessive aliases before rendering.
  return doc
}
export function readCatalog(source:string):{proxies:Entry[];groups:Entry[];providers:string[]} {
  const data=docOf(source).toJS({maxAliasCount:100})
  const list=(key:string)=>{if(data[key]===undefined)return [];if(!Array.isArray(data[key])||data[key].some((x:any)=>!x||typeof x!=='object'||Array.isArray(x)||typeof x.name!=='string'||typeof x.type!=='string'))throw new Error(key+' 必须是含名称与类型的列表');return data[key]}
  return {proxies:list('proxies'),groups:list('proxy-groups'),providers:Object.keys(data['proxy-providers']||{})}
}
// Visit only reference-bearing fields; passwords/URLs and arbitrary text are never replaced.
function references(doc:ReturnType<typeof docOf>,old:string,next?:string):string[] {
  const found:string[]=[]
  const scalar=(path:any[],label:string)=>{const n=doc.getIn(path,true);if(isScalar(n)&&n.value===old){found.push(label);if(next!==undefined){assertEditable(n);n.value=next}}}
  for(const kind of ['proxies','proxy-groups'] as Kind[]) {
    const seq=doc.get(kind,true);if(!isSeq(seq))continue
    seq.items.forEach((entry,i)=>{
      if(!isMap(entry))throw new Error('列表使用 YAML 别名；请先在 YAML 编辑器展开后再修改引用')
      if(kind==='proxies')scalar([kind,i,'dialer-proxy'],'前置代理')
      else {
        const members=entry.get('proxies',true);if(isSeq(members))members.items.forEach((_,j)=>scalar([kind,i,'proxies',j],'代理组成员'))
        for(const key of ['default-selected','empty-fallback'])scalar([kind,i,key],key)
      }
    })
  }
  for(const kind of ['proxy-providers','rule-providers']) {
    const map=doc.get(kind,true);if(isMap(map))for(const pair of map.items)scalar([kind,String(pair.key),'proxy'],'集合下载代理')
  }
  function rulesAt(path:any[]) {
    const rules=doc.getIn(path,true);if(!isSeq(rules))return
    for(const item of rules.items) {
      if(!isScalar(item)||typeof item.value!=='string')continue
      const chunks:string[]=[];let depth=0,start=0
      for(let i=0;i<item.value.length;i++){const c=item.value[i];if(c==='(')depth++;if(c===')')depth--;if(c===','&&depth===0){chunks.push(item.value.slice(start,i));start=i+1}}
      chunks.push(item.value.slice(start));if(chunks[0]==='SUB-RULE')continue
      const idx=chunks.at(-1)?.trim()==='no-resolve'?chunks.length-2:chunks.length-1
      if(idx>0&&chunks[idx].trim()===old){found.push('分流规则');if(next!==undefined){assertEditable(item);chunks[idx]=next;item.value=chunks.join(',')}}
    }
  }
  rulesAt(['rules']);const subs=doc.get('sub-rules',true);if(isMap(subs))for(const p of subs.items)rulesAt(['sub-rules',String(p.key)])
  return found
}
function checkName(name:any,source:string,old?:string) {
  if(typeof name!=='string'||!name.trim()||name!==name.trim()||name.length>200||/[\r\n,\x00]/.test(name)||builtins.includes(name))throw new Error('名称不能为空、保留字或含逗号/换行，且不超过 200 字符')
  const c=readCatalog(source);if([...c.proxies,...c.groups].some(p=>p.name===name&&p.name!==old))throw new Error('节点与代理组名称不能重复')
}
function assertEditable(node:any) {
  let complex=false
  visit(node,{Node(_key,n){if(isAlias(n)||(n as any).anchor)complex=true}})
  if(complex)throw new Error('此项使用共享 YAML 锚点或别名；请先在 YAML 编辑器展开，避免修改其他引用')
}
function patchMap(doc:ReturnType<typeof docOf>,path:any[],old:Entry,next:Entry) {
  for(const key of new Set([...Object.keys(old),...Object.keys(next)])) {
    if(JSON.stringify(old[key])===JSON.stringify(next[key]))continue
    if(!(key in next)){doc.deleteIn([...path,key]);continue}
    const a=old[key],b=next[key]
    if(a&&b&&typeof a==='object'&&typeof b==='object'&&!Array.isArray(a)&&!Array.isArray(b)&&isMap(doc.getIn([...path,key],true)))patchMap(doc,[...path,key],a,b)
    else {
      const previous=doc.getIn([...path,key],true),replacement=doc.createNode(b)
      if(isScalar(previous)&&isScalar(replacement)) previous.value=replacement.value
      else {
        if(previous&&typeof previous==='object')for(const k of ['comment','commentBefore','spaceBefore'] as const)(replacement as any)[k]=(previous as any)[k]
        doc.setIn([...path,key],replacement)
      }
    }
  }
}
export function applyEntry(source:string,kind:Kind,index:number,value:Entry,protectedNames:string[]=[]):string {
  const doc=docOf(source),c=readCatalog(source),list=kind==='proxies'?c.proxies:c.groups,old=list[index]
  if(index>=0&&!old)throw new Error('编辑项已不存在')
  checkName(value.name,source,old?.name)
  if(typeof value.type!=='string'||!value.type)throw new Error('请选择协议或策略类型')
  if(old&&old.name!==value.name&&protectedNames.includes(old.name))throw new Error('此名称被插件分流/设备/覆写设置引用，请保留此名称；如需替换，先新增目标并调整插件引用')
  if(kind==='proxies'&&value.port!==undefined&&(!Number.isInteger(value.port)||value.port<1||value.port>65535))throw new Error('端口必须为 1–65535 的整数')
  if(kind==='proxy-groups') {
    for(const k of ['interval','timeout','tolerance','max-failed-times'])if(value[k]!==undefined&&(!Number.isInteger(value[k])||value[k]<0))throw new Error(k+' 必须为非负整数')
    if(value.url){let u;try{u=new URL(value.url)}catch{throw new Error('测速地址无效')}if(!['http:','https:'].includes(u.protocol))throw new Error('测速地址必须是 HTTP(S)')}
  }
  let seq=doc.get(kind,true);if(!seq){doc.set(kind,doc.createNode([]));seq=doc.get(kind,true)}
  if(!isSeq(seq))throw new Error('配置列表无效')
  if(old){assertEditable(seq.items[index]);if(!isMap(seq.items[index]))throw new Error('此项使用 YAML 别名，请在 YAML 编辑器展开后修改');patchMap(doc,[kind,index],old,value);if(old.name!==value.name)references(doc,old.name,value.name)}
  else seq.add(doc.createNode(value))
  const out=String(doc);validateGraph(out);return out
}
export function removeEntry(source:string,kind:Kind,index:number,replacement:string,protectedNames:string[]=[]):string {
  const doc=docOf(source),c=readCatalog(source),entry=(kind==='proxies'?c.proxies:c.groups)[index]
  if(!entry)throw new Error('编辑项已不存在')
  if(protectedNames.includes(entry.name))throw new Error('此项被插件设置引用，不能在这里删除')
  assertEditable(doc.getIn([kind,index],true));const refs=references(doc,entry.name)
  if(refs.length){const names=[...builtins,...c.proxies.map(p=>p.name),...c.groups.map(p=>p.name)];if(!names.includes(replacement)||replacement===entry.name)throw new Error('此项被引用，请选择替代目标');references(doc,entry.name,replacement)}
  doc.deleteIn([kind,index]);
  // Replacing a removed member may introduce duplicates; retain its earliest priority.
  const groups=doc.get('proxy-groups',true);if(isSeq(groups))for(const group of groups.items){if(!isMap(group))continue;const members=group.get('proxies',true);if(isSeq(members)){const seen=new Set();members.items=members.items.filter(n=>{if(!isScalar(n))return true;if(seen.has(n.value))return false;seen.add(n.value);return true})}}
  const out=String(doc);validateGraph(out);return out
}
export function validateGraph(source:string) {
  const c=readCatalog(source),all=[...c.proxies,...c.groups],names=new Set([...builtins,...all.map(p=>p.name)])
  if(new Set(all.map(p=>p.name)).size!==all.length)throw new Error('存在重复节点/组名称')
  const edges=new Map<string,string[]>()
  for(const p of all){
    const members=p['dialer-proxy']?[p['dialer-proxy']]:[]
    if(c.groups.includes(p)) {
      if(p.proxies!==undefined&&(!Array.isArray(p.proxies)||p.proxies.some((n:any)=>typeof n!=='string')))throw new Error('代理组成员必须是列表')
      members.push(...(p.proxies||[]))
      if(p.use!==undefined&&(!Array.isArray(p.use)||p.use.some((x:string)=>!c.providers.includes(x))))throw new Error('引用了不存在的代理集合')
      if(!members.length&&!p.use?.length&&!p['include-all']&&!p['include-all-proxies']&&!p['include-all-providers'])throw new Error('代理组至少选择一个成员或集合')
    }
    // Provider-loaded node names may be referenced by an existing configuration.
    if(!c.providers.length&&members.some((n:string)=>!names.has(n)))throw new Error('引用了不存在的节点或代理组')
    edges.set(p.name,members)
  }
  const active=new Set<string>(),done=new Set<string>()
  function visit(n:string){if(active.has(n))throw new Error('代理组或前置代理存在循环引用');if(done.has(n)||!edges.has(n))return;active.add(n);for(const child of edges.get(n)!)visit(child);active.delete(n);done.add(n)}
  for(const name of edges.keys())visit(name)
}
