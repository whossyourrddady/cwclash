<script setup lang="ts">
import {ref,computed,onMounted,onUnmounted} from 'vue'
import {parseDocument,stringify} from 'yaml'
import EditorField from './EditorField.vue'
import {readCatalog,applyEntry,removeEntry,validateGraph,proxyTypes,groupTypes,proxyFields,transportFields,groupFields,builtins} from './proxy-editor'
import type {Entry,Kind} from './proxy-editor'
import type {Profile} from './api'
const props=defineProps<{content:string;profile:Profile;busy:boolean;protectedNames:string[];hasOverride:boolean}>()
const emit=defineEmits<{close:[];save:[data:{content:string;asCopy:boolean;name:string} ]}>()
const source=ref(props.content),error=ref(''),kind=ref<Kind>('proxies'),query=ref(''),draft=ref<Entry|null>(null),index=ref(-1),baseline=ref(''),advanced=ref(''),reveal=ref(false),closing=ref(false),deleting=ref<number|null>(null),replacement=ref('DIRECT'),asCopy=ref(!!props.profile.url),copyName=ref(props.profile.name+' · 本地编辑'),limit=ref(100)
const catalog=computed(()=>{try{return readCatalog(source.value)}catch(e:any){return {proxies:[],groups:[],providers:[]}}})
const entries=computed(()=>kind.value==='proxies'?catalog.value.proxies:catalog.value.groups)
const filtered=computed(()=>entries.value.map((p,i)=>({p,i})).filter(({p})=>(p.name+' '+(p.server||'')).toLowerCase().includes(query.value.toLowerCase())))
const advancedBaseline=ref('')
const advancedDirty=computed(()=>!!draft.value && advanced.value!==advancedBaseline.value)
const editDirty=computed(()=>!!draft.value && (JSON.stringify(draft.value)!==baseline.value||advancedDirty.value))
const dirty=computed(()=>source.value!==props.content||editDirty.value)
const choices=computed(()=>[...builtins,...catalog.value.proxies.map(p=>p.name),...catalog.value.groups.map(p=>p.name)].filter(n=>n!==draft.value?.name))
const types=computed(()=>[...new Set([...(kind.value==='proxies'?proxyTypes:groupTypes),draft.value?.type].filter(Boolean))])
const booleans=computed(()=>kind.value==='proxies'?['udp','tls','skip-cert-verify','tfo','mptcp']:['lazy','disable-udp','include-all-proxies','include-all-providers','hidden'])
const boolLabels:Record<string,string>={udp:'UDP',tls:'TLS','skip-cert-verify':'跳过证书验证',tfo:'TCP Fast Open',mptcp:'MPTCP',lazy:'按需检测','disable-udp':'禁用 UDP','include-all-proxies':'包含所有静态节点','include-all-providers':'包含所有集合',hidden:'隐藏组'}
function safe(fn:()=>void){try{error.value='';fn()}catch(e:any){error.value=e.message||'操作失败'}}
function canLeave(){if(editDirty.value){error.value='当前表单尚未应用，请先应用到草稿或取消此项编辑';return false}return true}
function tab(k:Kind){if(!canLeave())return;kind.value=k;draft.value=null;query.value='';limit.value=100;deleting.value=null}
function choose(i:number,copy=false){if(!canLeave())return;safe(()=>{draft.value=structuredClone(entries.value[i]);index.value=copy?-1:i;if(copy)draft.value!.name+=' · 副本';baseline.value=copy?'':JSON.stringify(draft.value);advanced.value=stringify(draft.value);advancedBaseline.value=advanced.value;reveal.value=false;deleting.value=null})}
function defaults(type:string){const common=kind.value==='proxies'?{name:'新节点',type,...(!['direct','reject','dns','rematch','tailscale','zerotier','easytier'].includes(type)?{server:'',port:443}:{}),...(type==='vmess'?{alterId:0,cipher:'auto'}:{}),...(type==='ss'?{cipher:'aes-128-gcm'}:{})}:{name:'新代理组',type,proxies:['DIRECT'],...(type!=='select'?{url:'https://www.gstatic.com/generate_204',interval:300}:{})};return common}
function add(){if(!canLeave())return;index.value=-1;draft.value=defaults(kind.value==='proxies'?'ss':'select');baseline.value='';advanced.value=stringify(draft.value);advancedBaseline.value=advanced.value;reveal.value=false}
function changeType(e:Event){const name=draft.value!.name;draft.value={...defaults((e.target as HTMLSelectElement).value),name};advanced.value=stringify(draft.value);advancedBaseline.value=advanced.value}
function apply(){safe(()=>{if(advancedDirty.value)throw new Error('请先同步高级 YAML 到表单，或重新载入表单值');source.value=applyEntry(source.value,kind.value,index.value,draft.value!,props.protectedNames);draft.value=null;baseline.value='';advanced.value='';reveal.value=false})}
function remove(){safe(()=>{source.value=removeEntry(source.value,kind.value,deleting.value!,replacement.value,props.protectedNames);deleting.value=null;draft.value=null})}
function members(key:'proxies'|'use'):string[]{const value=draft.value?.[key];return Array.isArray(value)?value.filter(v=>typeof v==='string'):[]}
function toggle(key:'proxies'|'use',name:string){const list=[...members(key)];const i=list.indexOf(name);if(i<0)list.push(name);else list.splice(i,1);draft.value![key]=list}
function move(key:'proxies'|'use',i:number,delta:number){const list=[...members(key)];if(i+delta<0||i+delta>=list.length)return;[list[i],list[i+delta]]=[list[i+delta],list[i]];draft.value![key]=list}
function boolChange(k:string,e:Event){const v=(e.target as HTMLSelectElement).value;if(v==='')delete draft.value![k];else draft.value![k]=v==='true'}
function applyAdvanced(){safe(()=>{const d=parseDocument(advanced.value,{merge:true,prettyErrors:false});if(d.errors.length)throw new Error('高级 YAML 语法无效');const value=d.toJS({maxAliasCount:100});if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('高级 YAML 必须是单个节点或组的映射');if(typeof value.name!=='string'||typeof value.type!=='string')throw new Error('名称和类型必须是字符串');if(kind.value==='proxy-groups')for(const key of ['proxies','use'])if(value[key]!==undefined&&(!Array.isArray(value[key])||value[key].some((x:any)=>typeof x!=='string')))throw new Error('组成员与集合必须是名称列表');draft.value=value;advancedBaseline.value=advanced.value})}
function openAdvanced(event:Event){if((event.target as HTMLDetailsElement).open&&!advancedDirty.value){advanced.value=stringify(draft.value);advancedBaseline.value=advanced.value}}
function save(){safe(()=>{if(editDirty.value)throw new Error('请先将当前表单应用到草稿');validateGraph(source.value);if(asCopy.value&&!copyName.value.trim())throw new Error('请填写副本名称');emit('save',{content:source.value,asCopy:asCopy.value,name:copyName.value.trim()})})}
function close(){if(props.busy)return;if(dirty.value)closing.value=true;else emit('close')}
function exportDraft(){const blob=new Blob([source.value],{type:'text/yaml'}),url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='cwclash-editor-draft.yaml';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}
function unload(e:BeforeUnloadEvent){if(dirty.value){e.preventDefault();e.returnValue=''}}
onMounted(()=>{safe(()=>readCatalog(source.value));window.addEventListener('beforeunload',unload)})
onUnmounted(()=>window.removeEventListener('beforeunload',unload))
</script>
<template>
<div class="modal-backdrop"><section class="modal visual-editor" role="dialog" aria-modal="true" aria-labelledby="visual-title">
 <div class="section-heading"><div><h2 id="visual-title">节点与代理组编辑</h2><p>{{ profile.name }} · {{ dirty ? '有未保存草稿' : '已载入原始配置' }}</p></div><button class="button button-secondary" :disabled="busy" @click="close">关闭编辑</button></div>
 <p class="field-help">修改原始配置；服务停止时也能编辑。字段留空使用内核默认值；其他高级字段会保留。保存前由 mihomo 检查，当前配置变更会短暂重启代理。</p>
 <p v-if="hasOverride" class="banner">当前有全局 YAML 覆写，可能覆盖这里的节点或组。这里编辑的是原始配置，最终生效结果以运行配置为准。</p>
 <p v-if="error" class="banner negative" role="alert">{{ error }}</p>
 <div v-if="closing" class="banner">放弃未保存的草稿并关闭？<div class="button-row"><button class="button button-secondary" @click="closing=false">继续编辑</button><button class="button button-danger" @click="emit('close')">放弃草稿并关闭</button></div></div>
 <fieldset :disabled="busy" class="editor-fieldset">
 <div class="tabs"><button :class="{active:kind==='proxies'}" @click="tab('proxies')">编辑节点（{{ catalog.proxies.length }}）</button><button :class="{active:kind==='proxy-groups'}" @click="tab('proxy-groups')">编辑代理组（{{ catalog.groups.length }}）</button></div>
 <div class="toolbar"><input v-model="query" aria-label="搜索编辑项" placeholder="搜索名称或服务器" /><button class="button button-secondary" @click="add">{{ kind==='proxies'?'新增节点':'新增代理组' }}</button></div>
 <div class="editor-list"><div v-for="{p,i} in filtered.slice(0,limit)" :key="i" class="provider-row"><div><strong>{{ p.name }}</strong><small>{{ p.type }}{{ p.server?' · '+p.server+':'+(p.port||'') : '' }}</small></div><div class="button-row"><button class="button button-secondary button-small" :aria-label="'编辑 '+p.name" @click="choose(i)">编辑</button><button class="button button-secondary button-small" :aria-label="'复制 '+p.name" @click="choose(i,true)">复制</button><button class="button button-secondary button-small" :aria-label="'删除 '+p.name" @click="canLeave() && (deleting=i)">删除</button></div></div><p v-if="!filtered.length" class="muted">没有匹配项。在线代理集合中的节点由集合维护，不在此修改缓存。</p><button v-if="filtered.length>limit" class="text-button" @click="limit+=100">显示更多（还有 {{ filtered.length-limit }} 项）</button></div>
 <div v-if="deleting!==null" class="banner"><p>删除 {{ entries[deleting]?.name }}？若有配置内引用，将替换为下方目标；未保存前可关闭编辑放弃整份草稿。</p><label>替代目标<select v-model="replacement" aria-label="替代目标"><option v-for="n in [...builtins,...catalog.proxies.map(p=>p.name),...catalog.groups.map(p=>p.name)].filter(n=>n!==entries[deleting!]?.name)" :key="n">{{ n }}</option></select></label><div class="button-row"><button class="button button-secondary" @click="deleting=null">取消删除</button><button class="button button-danger" @click="remove">确认删除此项</button></div></div>
 <div v-if="draft" class="card editor-form">
 <h3>{{ index<0?'新增':'编辑' }}{{ kind==='proxies'?'节点':'代理组' }}</h3>
 <div class="form-grid"><label>名称<input v-model="draft.name" maxlength="200" /></label><label>{{ kind==='proxies'?'协议类型':'策略类型' }}<select :value="draft.type" :aria-label="kind==='proxies'?'协议类型':'策略类型'" :disabled="index>=0" @change="changeType"><option v-for="t in types" :key="t">{{ t }}</option></select></label><EditorField v-for="field in (kind==='proxies'?proxyFields(draft.type):groupFields.filter(f=>['url','interval','timeout'].includes(f.key)))" :key="field.key" :field="field" :value="draft" :reveal="reveal" /></div>
 <p class="field-help">已有项的类型保持不变；如需换协议/策略，请新增或复制为新项。复制项换类型会清除协议专属字段。</p>
 <template v-if="kind==='proxy-groups'">
 <div v-for="key in (['proxies','use'] as const)" :key="key" class="editor-members"><h4>{{ key==='proxies'?'成员与优先顺序':'代理集合与顺序' }}</h4><div v-for="(name,i) in members(key)" :key="name" class="editor-member"><span>{{ i+1 }}. {{ name }}</span><button class="button button-secondary button-small" :disabled="i===0" :aria-label="'上移 '+name" @click="move(key,i,-1)">↑</button><button class="button button-secondary button-small" :disabled="i===members(key).length-1" :aria-label="'下移 '+name" @click="move(key,i,1)">↓</button><button class="button button-secondary button-small" :aria-label="'移除 '+name" @click="toggle(key,name)">移除</button></div><div class="editor-choices"><label v-for="name in (key==='proxies'?choices:catalog.providers)" :key="name"><input type="checkbox" :checked="members(key).includes(name)" @change="toggle(key,name)" />{{ name }}</label></div><p v-if="key==='use'&&!catalog.providers.length" class="field-help">配置没有 proxy-providers 集合。</p></div>
 </template>
 <details v-if="kind==='proxy-groups'"><summary>筛选与高级策略参数</summary><div class="form-grid"><EditorField v-for="field in groupFields.filter(f=>!['url','interval','timeout'].includes(f.key))" :key="field.key" :field="field" :value="draft" /></div><p class="field-help">切换容差用于 url-test，负载均衡策略和 Hash Key 用于 load-balance。最终支持情况由当前内核检查。</p></details>
 <details v-if="kind==='proxies'"><summary>传输、TLS 与前置代理</summary><div class="form-grid"><EditorField v-for="field in transportFields" :key="field.key" :field="field" :value="draft" :reveal="reveal" /></div><p class="field-help">不同协议支持的字段不同；复杂 peers、插件、证书及多路径可在高级 YAML 中编辑。</p></details>
 <div class="form-grid"><label v-for="key in booleans" :key="key">{{ boolLabels[key] }}<select :aria-label="boolLabels[key]" :value="draft[key]===undefined?'':String(draft[key])" @change="boolChange(key,$event)"><option value="">沿用默认</option><option value="true">开启</option><option value="false">关闭</option></select></label></div>
 <label v-if="kind==='proxies'" class="check-setting"><input v-model="reveal" type="checkbox" />显示当前节点凭据</label>
 <details @toggle="openAdvanced"><summary>高级 YAML（含凭据）</summary><p class="field-help">仅编辑当前项；用于没有专用表单的协议参数。修改后点击同步，再应用到草稿。</p><textarea v-model="advanced" aria-label="当前项高级 YAML" class="code-editor" rows="9" spellcheck="false" /><button class="button button-secondary" @click="applyAdvanced">同步高级 YAML 到表单</button></details>
 <div class="button-row"><button class="button button-primary" @click="apply">应用到草稿</button><button class="button button-secondary" @click="draft=null;baseline=''">取消此项编辑</button></div>
 </div>
 <div class="editor-save"><label class="check-setting"><input v-model="asCopy" type="checkbox" />另存为本地配置副本</label><label v-if="asCopy">副本名称<input v-model="copyName" maxlength="100" /></label><p v-if="profile.url" class="field-help">直接保存在线订阅后，下次订阅更新会覆盖修改。推荐另存本地副本；副本不自动订阅更新，也不自动切换当前配置。</p><div class="button-row"><button class="button button-primary" :disabled="busy||editDirty||(!dirty&&!asCopy)" @click="save">{{ busy?'正在校验…':'校验并保存编辑' }}</button><button class="button button-secondary" @click="exportDraft">导出已应用的 YAML 草稿</button></div><p class="field-help">草稿含节点凭据，仅保留在当前页面；高级 YAML 未同步或表单未应用的内容不包含在导出中。</p></div>
 </fieldset>
</section></div>
</template>
