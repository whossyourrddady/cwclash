<script setup lang="ts">
import {ref,computed,onMounted,onUnmounted} from 'vue'
import {rpc,actionJob} from './api'
const props=defineProps<{connected:boolean;preview?:boolean}>()
const data=ref<any>({reports:{},jobs:{},stale:{},sites:[],media:[]}),busy=ref(''),error=ref(''),automatic=ref(readAuto())
let timer:ReturnType<typeof setInterval>,alive=true,lastAttempt=0,reading=false
function readAuto(){try{return localStorage.getItem('cwclash.connectivity.auto')!=='false'}catch{return true}}
function saveAuto(){try{localStorage.setItem('cwclash.connectivity.auto',String(automatic.value))}catch{} }
const anyBusy=computed(()=>!!busy.value||Object.keys(data.value.jobs||{}).length>0)
const basic=computed(()=>data.value.reports?.basic),media=computed(()=>data.value.reports?.media)
const stale=(kind:string)=>!props.connected||!!error.value||data.value.stale?.[kind]
const results=(kind:string)=>data.value.reports?.[kind]?.results||[]
const item=(kind:string,id:string)=>results(kind).find((x:any)=>x.id===id)
const sites=computed(()=>data.value.sites.filter((x:any)=>['sites','ai'].includes(x.group)))
const direct=computed(()=>results('basic').filter((x:any)=>x.group==='direct'))
const internet=computed(()=>!basic.value?'未检测':stale('basic')?'结果待刷新':direct.value.some((x:any)=>x.state==='reachable')?'已连通':direct.value.some((x:any)=>x.state==='limited')?'已响应 · 有限制':'未连通')
const stamp=(report:any)=>report?.finishedAt?new Date(report.finishedAt).toLocaleTimeString('zh-CN',{hour12:false}):'尚未检测'
function label(result:any,kind:string){if(!result)return anyBusy.value?'等待检测':'未检测';if(stale(kind))return '结果待刷新';if(kind==='media'){if(result.unlock==='restricted')return '地区受限';if(result.unlock==='likely')return '疑似可用';if(result.state==='reachable')return '解锁待验证'}return ({reachable:'已连通',limited:'访问受限',failed:'未连通',unknown:'无法判断'} as Record<string,string>)[result.state]||'未检测'}
function color(result:any,kind:string){if(!result||stale(kind))return 'muted';if(kind==='media'&&result.unlock==='restricted')return 'negative';if(kind==='media'&&result.unlock==='likely')return 'warning';return result.state==='reachable'?(kind==='media'?'warning':'positive'):result.state==='failed'?'negative':'warning'}
function linkLabel(link:any){if(!basic.value||!link)return '未检测';if(stale('basic'))return '结果待刷新';return ({ready:'接口就绪',down:'未接通',limited:'未取得地址',unknown:'状态未知'} as Record<string,string>)[link.state]||'状态未知'}
function linkColor(link:any){if(stale('basic')||!link)return 'muted';return link.state==='ready'?'positive':link.state==='down'?'negative':link.state==='limited'?'warning':'muted'}
async function read(){if(reading)return;reading=true;try{const next=await rpc('connectivity.get');if(alive){data.value=next;error.value=''}}catch(e:any){if(alive)error.value=e.message}finally{reading=false}}
async function check(kind:string){if(anyBusy.value||!props.connected)return;busy.value=kind;lastAttempt=Date.now();error.value='';try{await actionJob(kind==='media'?'connectivity.media':'connectivity.check');await read()}catch(e:any){if(alive)error.value=e.message}finally{busy.value=''}}
async function tick(){if(document.hidden||!props.connected)return;await read();if(!alive||error.value)return;const finished=Date.parse(basic.value?.finishedAt||'');if(automatic.value&&!anyBusy.value&&Date.now()-lastAttempt>60000&&(!finished||Date.now()-finished>300000||stale('basic')))void check('basic')}
onMounted(()=>{void tick();timer=setInterval(tick,10000)})
onUnmounted(()=>{alive=false;clearInterval(timer)})
</script>
<template>
<section class="card connectivity-panel" aria-label="网络与网站连通状态">
 <div class="section-heading"><div><h3>网络与网站连通</h3><p>{{ basic?.route||'检测尚未开始' }}{{ preview?' · 仅测速，未接管家庭网络':'' }} · {{ stamp(basic) }}</p></div><button class="button button-secondary button-small" :disabled="anyBusy||!connected" @click="check('basic')">{{ busy==='basic'?'正在检测…':'检测连通性' }}</button></div>
 <p v-if="error" class="banner negative" role="alert">检测暂不可用：{{ error }}</p>
 <div class="connectivity-links">
  <article><span>内网 LAN</span><strong :class="linkColor(basic?.lan)">{{ linkLabel(basic?.lan) }}</strong><small>{{ basic?.lan?.name||'网桥待识别' }} · {{ connected?'管理服务可达':'管理服务未连接' }}</small><small>{{ basic?.lan?.addresses?.join(' · ') }}</small></article>
  <article><span>外网 WAN 接口</span><strong :class="linkColor(basic?.wan)">{{ linkLabel(basic?.wan) }}</strong><small>{{ basic?.wan?.detail||'等待读取路由器接口' }}</small><small>{{ basic?.wan?.addresses?.join(' · ') }}</small><small v-if="basic?.wan">物理链路：{{ ({up:'已接通',down:'未接通',unknown:'固件未提供'} as Record<string,string>)[basic.wan.carrier] }}</small></article>
  <article><span>外网直连</span><strong :class="internet==='已连通'?'positive':internet==='未连通'?'negative':'muted'">{{ internet }}</strong><small>路由器直连百度 / 阿里云</small><small v-for="r in direct" :key="r.id">{{ r.name }}：{{ label(r,'basic') }}</small></article>
 </div>
 <div class="connectivity-sites"><article v-for="site in sites" :key="site.id"><div><strong>{{ site.name }}</strong><span :class="color(item('basic',site.id),'basic')">{{ label(item('basic',site.id),'basic') }}</span></div><small v-if="item('basic',site.id)">{{ typeof item('basic',site.id).latencyMs === 'number' ? `${item('basic',site.id).latencyMs} ms` : '耗时未知' }}<span v-if="item('basic',site.id).http"> · HTTP {{ item('basic',site.id).http }}</span></small><small>{{ item('basic',site.id)?.detail||'等待首次检测' }}</small></article></div>
 <div class="connectivity-footer"><label class="check-setting"><input v-model="automatic" type="checkbox" @change="saveAuto" />概览可见时约每 5 分钟检测</label><small v-if="item('basic','exit')">Cloudflare 检测出口：{{ item('basic','exit').ip||'未取得' }} {{ item('basic','exit').region||'' }}{{ stale('basic')?' · 待刷新':'' }}</small></div>
 <p class="field-help">接口就绪不等于互联网可用。网站检测由路由器发起；代理运行时走 mihomo 当前规则，各网站可能分流到不同出口。HTTPS 可达不代表登录、AI 对话或所有设备的透明代理均可用。</p>
 <details class="media-checks"><summary>流媒体地区 / 解锁检测 <span class="muted">{{ stamp(media) }}</span></summary><div class="section-heading"><p>{{ media?.route||'未检测' }} · 手动检测，不播放视频</p><button class="button button-secondary button-small" :disabled="anyBusy||!connected" @click="check('media')">{{ busy==='media'?'正在检测流媒体…':'检测流媒体' }}</button></div>
 <div class="connectivity-sites"><article v-for="site in data.media" :key="site.id"><div><strong>{{ site.name }}</strong><span :class="color(item('media',site.id),'media')">{{ label(item('media',site.id),'media') }}</span></div><small v-if="item('media',site.id)?.region">站点地区：{{ item('media',site.id).region }}</small><small>{{ item('media',site.id)?.detail||'尚未检测' }}</small></article></div><p class="field-help">Netflix / YouTube Premium 的“疑似可用”来自公开页面信号，不是账号播放验收。Disney+、Spotify、Prime Video、Max、Apple TV、Twitch 无充分证据时显示待验证；地区受限只在页面明确提示时显示。切换线路后请重新检测。</p></details>
</section>
</template>
