import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from '../web/node_modules/typescript/lib/typescript.js'
const js = ts.transpileModule(await readFile('web/src/presentation.ts', 'utf8'), { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText
const { subscriptionSummary: quota, orderNodes, currentDelay, historyKey, defaultTestURL } = await import('data:text/javascript;base64,' + Buffer.from(js).toString('base64'))
assert.equal(quota('').remaining, null)
assert.equal(quota('total=0;expire=0').expiry, null)
assert.equal(quota('upload=-2;download=NaN;total=99999999999999999999').used, null)
assert.deepEqual(quota('upload=20;download=90;total=100;expire=1', 2000), { used:110,total:100,remaining:0,percent:100,expiry:1000,expired:true })
assert.equal(quota('total=100').remaining, null)
const original = ['unknown','failed','slow','fast']
const delays = {unknown:null,failed:0,slow:300,fast:40}
assert.deepEqual(orderNodes(original,'delay', n=>delays[n]), ['fast','slow','unknown','failed'])
assert.deepEqual(original,['unknown','failed','slow','fast'])
assert.deepEqual(orderNodes(original,'default',n=>delays[n]),original)
console.log('Presentation edge cases passed: missing/invalid quota, expiry, overage, delay ordering and immutable input')
const before = [{time:'a',delay:20}]
const result = {delay:48,baseline:historyKey(before),url:defaultTestURL}
assert.equal(currentDelay(result,before,defaultTestURL),48)
assert.equal(currentDelay(result,[{time:'b',delay:777}],defaultTestURL),777)
assert.equal(currentDelay({...result,delay:0},before,defaultTestURL),0)
assert.equal(currentDelay(result,before,'https://example.test/'),null)
assert.equal(currentDelay({...result,url:'https://example.test/'},before,'https://example.test/'),48)
assert.equal(currentDelay(undefined,[],defaultTestURL),null)
console.log('Delay results expire on new core history and do not cross test URLs')
