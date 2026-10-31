import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from '../web/node_modules/typescript/lib/typescript.js'
const js = ts.transpileModule(await readFile('web/src/presentation.ts', 'utf8'), { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText
const { subscriptionSummary: quota, orderNodes } = await import('data:text/javascript;base64,' + Buffer.from(js).toString('base64'))
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
