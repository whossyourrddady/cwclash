import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from '../web/node_modules/typescript/lib/typescript.js'
const js = ts.transpileModule(await readFile('web/src/api.ts','utf8'),{compilerOptions:{module:ts.ModuleKind.ESNext}}).outputText
const {actionJob} = await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'))
const originalFetch = globalThis.fetch
let calls=[], fail=false, resolveFinal, finish
globalThis.fetch = async (_, options) => {
  const request=JSON.parse(options.body), action=request.params[0]
  const args=JSON.parse(Buffer.from(request.params[1],'base64').toString())
  calls.push({action,args})
  let data
  switch(action) {
    case 'profiles.save': data={jobId:'save'};break
    case 'upload.begin': data={id:'upload',chunkSize:768};break
    case 'upload.chunk': data={};break
    case 'upload.finish': data={jobId:'outer'};break
    case 'jobs.get':
      assert.ok(args.id,'Must never poll an undefined job ID')
      if(args.id==='outer') data={status:'done',result:{jobId:'save'}}
      else { resolveFinal(); await finish; data=fail ? {status:'error',message:'inner validation failed'} : {status:'done',result:{id:'profile'}} }
      break
    default: assert.fail('Unexpected action '+action)
  }
  return new Response(JSON.stringify({result:Buffer.from(JSON.stringify({ok:true,data})).toString('base64')}))
}
try {
  for(const large of [false,true]) for(const failure of [false,true]) {
    calls=[]; fail=failure
    let signal, completed=false
    const entered=new Promise(r=>resolveFinal=r);finish=new Promise(r=>signal=r)
    const task=actionJob('profiles.save',{name:'test',content:large?'x'.repeat(1800):'small'})
    task.then(()=>completed=true,()=>completed=true)
    await entered
    assert.equal(completed,false,'Must wait for final save, not upload completion')
    signal()
    if(failure) await assert.rejects(task,/inner validation failed/)
    else assert.deepEqual(await task,{id:'profile'})
    assert.equal(calls.some(c=>c.action==='upload.begin'),large)
    assert.equal(calls.filter(c=>c.action==='jobs.get'&&c.args.id==='save').length,1)
  }
  console.log('Small and chunked requests wait for final save; inner failures propagate; no undefined job polling')
} finally {globalThis.fetch=originalFetch}
