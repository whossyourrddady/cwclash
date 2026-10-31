import assert from 'node:assert/strict'
import {readFile,mkdir,writeFile,unlink} from 'node:fs/promises'
import {pathToFileURL} from 'node:url'
import {resolve} from 'node:path'
import {spawnSync} from 'node:child_process'
import ts from '../web/node_modules/typescript/lib/typescript.js'
import {parse} from '../web/node_modules/yaml/dist/index.js'
const modulePath=resolve('web/.proxy-editor-test.mjs')
const code=ts.transpileModule(await readFile('web/src/proxy-editor.ts','utf8'),{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
await writeFile(modulePath,code)
try {
 const {readCatalog,applyEntry,removeEntry,validateGraph,setField}=await import(pathToFileURL(modulePath))
 const raw=`# retain document comment
mixed-port: 0
external-controller: ''
dns: {enable: false}
proxy-providers: {}
proxies:
  - name: alpha # retain node comment
    type: socks5
    server: 127.0.0.1
    port: 32111
    username: fixture
    password: alpha
    udp: true
    x-extra: {retained: 123}
  - {name: beta, type: socks5, server: 127.0.0.1, port: 32112, dialer-proxy: alpha}
proxy-groups:
  - name: choice
    type: select
    proxies: [alpha, beta, DIRECT]
    default-selected: alpha
    x-group-extra: keep
rules:
  - DOMAIN,example.test,choice
  - AND,((NETWORK,UDP),(DST-PORT,443)),alpha
  - IP-CIDR,192.0.2.0/24,alpha,no-resolve
  - MATCH,choice
sub-rules:
  nested: ["MATCH,alpha"]
`
 const entry=readCatalog(raw).proxies[0];entry.name='renamed';entry.port=32113
 let out=applyEntry(raw,'proxies',0,entry)
 let data=parse(out)
 assert.equal(data.proxies[0].password,'alpha')
 assert.equal(data.proxies[0]['x-extra'].retained,123)
 assert.equal(data.proxies[1]['dialer-proxy'],'renamed')
 assert.deepEqual(data['proxy-groups'][0].proxies,['renamed','beta','DIRECT'])
 assert.equal(data['proxy-groups'][0]['default-selected'],'renamed')
 assert.match(out,/# retain document comment/);assert.match(out,/# retain node comment/)
 assert.equal(data.rules[1],'AND,((NETWORK,UDP),(DST-PORT,443)),renamed')
 assert.equal(data.rules[2],'IP-CIDR,192.0.2.0/24,renamed,no-resolve')
 assert.equal(data['sub-rules'].nested[0],'MATCH,renamed')
 let group=readCatalog(out).groups[0];group.name='routing';group.proxies=['beta','renamed','DIRECT']
 out=applyEntry(out,'proxy-groups',0,group)
 assert.equal(parse(out).rules.at(-1),'MATCH,routing')
 assert.equal(parse(out)['proxy-groups'][0]['x-group-extra'],'keep')
 assert.throws(()=>applyEntry(out,'proxies',0,{...entry,name:'beta'}),/重复/)
 assert.throws(()=>applyEntry(out,'proxies',0,{...entry,name:'DIRECT'}),/名称/)
 assert.throws(()=>applyEntry(out,'proxies',0,{...entry,name:'other'},['renamed']),/引用/)
 assert.throws(()=>applyEntry(out,'proxy-groups',0,{...group,proxies:['routing']}),/循环/)
 assert.throws(()=>applyEntry(out,'proxies',0,{...entry,'dialer-proxy':'beta'}),/循环/)
 assert.throws(()=>removeEntry(out,'proxies',0,'missing'),/替代目标/)
 assert.throws(()=>applyEntry(out,'proxies',0,{...entry,port:0}),/端口/)
 assert.throws(()=>applyEntry(out,'proxy-groups',0,{...group,use:['missing']}),/不存在/)
 assert.throws(()=>applyEntry(out,'proxy-groups',0,{...group,proxies:[]}),/至少/)
 const deleted=removeEntry(out,'proxies',0,'DIRECT')
 assert.equal(parse(deleted).proxies.length,1)
 assert.equal(parse(deleted).proxies[0]['dialer-proxy'],'DIRECT')
 assert.equal(parse(deleted).rules[1],'AND,((NETWORK,UDP),(DST-PORT,443)),DIRECT')
 const nested={type:'vless',name:'x','ws-opts':{path:'/old',headers:{Host:'example.test','X-Custom':'keep'}},uuid:'fixture'}
 setField(nested,'ws-opts.path','/new');setField(nested,'ws-opts.headers.Host','new.test')
 assert.equal(nested['ws-opts'].headers['X-Custom'],'keep')
 const anchored=raw.replace('- name: alpha','- &shared\n    name: alpha')
 assert.throws(()=>applyEntry(anchored,'proxies',0,{...entry,name:'other'}),/锚点/)
 for(const type of ['url-test','fallback','load-balance'])out=applyEntry(out,'proxy-groups',-1,{name:type,type,proxies:['renamed','beta'],url:'http://127.0.0.1:32199/check',interval:0})
 validateGraph(out)
 // A real native core validates the exact editor-produced YAML, without starting listeners or testing remote nodes.
 if(process.env.CWCLASH_TEST_CORE_PATH){
   const dir=resolve('.cache/visual-editor-core');await mkdir(dir,{recursive:true});const path=resolve(dir,'config.yaml');await writeFile(path,out)
   const r=spawnSync(process.env.CWCLASH_TEST_CORE_PATH,['-t','-d',dir,'-f',path],{encoding:'utf8',windowsHide:true,timeout:20000})
   assert.equal(r.status,0,r.stdout+r.stderr)
 }
 console.log('Visual editor: comments/unknown fields, nested edits, references, ordered members, duplicate/port/provider/cycle checks, deletion and anchor guards passed; native validation '+Boolean(process.env.CWCLASH_TEST_CORE_PATH))
} finally {await unlink(modulePath)}
