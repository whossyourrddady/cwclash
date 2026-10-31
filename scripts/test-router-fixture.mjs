// SPDX-License-Identifier: GPL-3.0-or-later
import assert from 'node:assert/strict'
import { connect } from 'node:net'
import { createSocket } from 'node:dgram'
import { startFixture } from './router-fixture.mjs'

const fixture=await startFixture({bind:'127.0.0.1',router:'127.0.0.1',output:'.cache/router-fixture'})
function connectViaSocks(address){return new Promise((resolve,reject)=>{
  const socket=connect(21809,'127.0.0.1');let stage=0,buffer=Buffer.alloc(0)
  socket.setTimeout(5000,()=>socket.destroy(Error('timeout')));socket.on('error',reject)
  socket.on('connect',()=>socket.write(Buffer.from([5,1,0])))
  socket.on('data',data=>{buffer=Buffer.concat([buffer,data]);if(stage===0){if(buffer.length<2)return;assert.equal(buffer[1],0);buffer=buffer.subarray(2);stage=1;socket.write(Buffer.from([5,1,0,1,...address,0,80]))}if(stage===1){if(buffer.length<10)return;const code=buffer[1];buffer=buffer.subarray(10);stage=2;if(code){socket.destroy();resolve({code});return}socket.write('GET / HTTP/1.1\r\nHost: cwclash.test\r\nConnection: close\r\n\r\n')}})
  socket.on('end',()=>resolve({code:0,body:buffer.toString()}))
})}
const exchange=packet=>new Promise((resolve,reject)=>{const udp=createSocket('udp4');const timeout=setTimeout(()=>{udp.close();reject(Error('UDP timeout'))},5000);udp.once('message',data=>{clearTimeout(timeout);udp.close();resolve(data)});udp.send(packet,21810,'127.0.0.1')})
try{
  assert.match((await connectViaSocks([203,0,113,44])).body,/cwclash-router-tcp-ok/)
  assert.equal((await connectViaSocks([203,0,113,45])).code,2)
  const packet=Buffer.from([0,0,0,1,203,0,113,44,85,50,...Buffer.from('udp-test')]);assert.ok((await exchange(packet)).toString().endsWith('cwclash-router-udp-ok'))
  const query=Buffer.from([1,2,1,0,0,1,0,0,0,0,0,0,7,...Buffer.from('cwclash'),4,...Buffer.from('test'),0,0,1,0,1]);assert.ok((await exchange(query)).subarray(-4).equals(Buffer.from([203,0,113,44])))
  assert.deepEqual(fixture.stats,{tcp:1,udp:1,dns:1,rejected:1})
  console.log('PASS fixture TCP relay, rejected non-test destination, SOCKS UDP and DNS reply')
}finally{await fixture.stop()}
