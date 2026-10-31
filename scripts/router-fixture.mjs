// SPDX-License-Identifier: GPL-3.0-or-later
// Controlled LAN endpoints. They only serve synthetic traffic for TEST-NET addresses.
import { createServer as httpServer } from 'node:http'
import { createServer as tcpServer, connect, isIP } from 'node:net'
import { createSocket } from 'node:dgram'
import { networkInterfaces } from 'node:os'
import { mkdir, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const ipv4 = Buffer.from([203,0,113,44])
const ipv6 = Buffer.from('20010db800000000000000000000000044','hex')
const normalize = value => value?.replace(/^::ffff:/,'')

export async function startFixture({bind='127.0.0.1',router='192.168.50.1',output='output/router-test'}={}) {
  const owned = Object.values(networkInterfaces()).flat().some(address=>address?.address===bind)
  if (isIP(bind)!==4 || !owned || isIP(router)!==4) throw Error('Bind must be an IPv4 address owned by this computer')
  const allowed = address => [router,bind,'127.0.0.1'].includes(normalize(address))
  const stats = {tcp:0,udp:0,dns:0,rejected:0}
  const sockets = new Set()
  const track = socket => { sockets.add(socket);socket.on('close',()=>sockets.delete(socket));socket.on('error',()=>undefined) }
  const echo = httpServer((req,res)=>{
    if(!allowed(req.socket.remoteAddress)){stats.rejected++;res.writeHead(403);res.end();return}
    if(req.url==='/metrics'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(stats));return}
    if(req.url==='/health'){res.writeHead(204);res.end();return}
    res.end('cwclash-router-tcp-ok')
  })
  echo.on('connection',track)
  const socks = tcpServer(socket=>{
    track(socket)
    if(!allowed(socket.remoteAddress)){stats.rejected++;socket.destroy();return}
    socket.setTimeout(180000,()=>socket.destroy())
    let phase=0,buffer=Buffer.alloc(0)
    const data = incoming=>{
      buffer=Buffer.concat([buffer,incoming])
      if(buffer.length>65536){socket.destroy();return}
      if(phase===0){if(buffer.length<2||buffer.length<2+buffer[1])return;if(buffer[0]!==5){socket.destroy();return}buffer=buffer.subarray(2+buffer[1]);socket.write(Buffer.from([5,0]));phase=1}
      if(phase!==1||buffer.length<4)return
      const command=buffer[1],type=buffer[3]
      let end,address
      if(type===1){end=10;if(buffer.length<end)return;address=buffer.subarray(4,8)}
      else if(type===4){end=22;if(buffer.length<end)return;address=buffer.subarray(4,20)}
      else if(type===3){end=7+buffer[4];if(buffer.length<end)return;address=buffer.subarray(5,5+buffer[4])}
      else{socket.destroy();return}
      if(command===3){phase=2;socket.off('data',data);socket.write(Buffer.from([5,0,0,1,...bind.split('.').map(Number),21810>>8,21810&255]));return}
      const port=buffer.readUInt16BE(end-2)
      const targetOK=(type===1&&address.equals(ipv4))||(type===4&&address.equals(ipv6))||(type===3&&address.toString()==='cwclash.test')
      if(command!==1||!targetOK||![80,21808].includes(port)){stats.rejected++;socket.end(Buffer.from([5,2,0,1,0,0,0,0,0,0]));return}
      phase=2;socket.off('data',data)
      const rest=buffer.subarray(end)
      const upstream=connect({host:bind,port:21808},()=>{stats.tcp++;socket.write(Buffer.from([5,0,0,1,...bind.split('.').map(Number),0,0]));if(rest.length)upstream.write(rest);socket.pipe(upstream);upstream.pipe(socket)})
      track(upstream);upstream.on('error',()=>socket.destroy());socket.on('close',()=>upstream.destroy())
    }
    socket.on('data',data)
  })
  const udp=createSocket('udp4')
  udp.on('message',(packet,peer)=>{
    if(!allowed(peer.address)){stats.rejected++;return}
    let end=0
    if(packet.length>=10&&packet[0]===0&&packet[1]===0&&packet[2]===0&&packet[3]===1&&packet.subarray(4,8).equals(ipv4))end=10
    if(packet.length>=22&&packet[0]===0&&packet[1]===0&&packet[2]===0&&packet[3]===4&&packet.subarray(4,20).equals(ipv6))end=22
    if(end){stats.udp++;udp.send(Buffer.concat([packet.subarray(0,end),Buffer.from('cwclash-router-udp-ok')]),peer.port,peer.address);return}
    if(packet.length<17)return
    let cursor=12;const labels=[]
    while(cursor<packet.length&&packet[cursor]!==0){const n=packet[cursor++];if(n>63||cursor+n>=packet.length)return;labels.push(packet.subarray(cursor,cursor+n).toString());cursor+=n}
    cursor++;if(cursor+4>packet.length)return
    const name=labels.join('.').toLowerCase(),type=packet.readUInt16BE(cursor),question=packet.subarray(12,cursor+4)
    if(name!=='cwclash.test'){stats.rejected++;return}
    stats.dns++
    const answer=type===1?ipv4:type===28?ipv6:null
    const header=Buffer.from(packet.subarray(0,12));header[2]=0x81;header[3]=0x80;header.writeUInt16BE(answer?1:0,6);header.writeUInt16BE(0,8);header.writeUInt16BE(0,10)
    const rr=answer?Buffer.concat([Buffer.from([0xc0,0x0c,type>>8,type&255,0,1,0,0,0,10,0,answer.length]),answer]):Buffer.alloc(0)
    udp.send(Buffer.concat([header,question,rr]),peer.port,peer.address)
  })
  const listen=(server,port)=>new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,bind,resolve)})
  try {
    await listen(echo,21808);await listen(socks,21809)
    await new Promise((resolve,reject)=>{udp.once('error',reject);udp.bind(21810,bind,resolve)})
    await mkdir(output,{recursive:true})
    const profile={dns:{'nameserver-policy':{'cwclash.test':[`udp://${bind}:21810`]}},proxies:[{name:'LAN-FIXTURE',type:'socks5',server:bind,port:21809,udp:true}],'proxy-groups':[{name:'长城实机测试',type:'select',proxies:['LAN-FIXTURE','DIRECT']}],rules:['IP-CIDR,203.0.113.44/32,长城实机测试,no-resolve','IP-CIDR6,2001:db8::44/128,长城实机测试,no-resolve','MATCH,DIRECT']}
    await writeFile(resolve(output,'router-test.yaml'),JSON.stringify(profile,null,2)+'\n')
  } catch(error){for(const socket of sockets)socket.destroy();echo.close();socks.close();try{udp.close()}catch{};throw error}
  return {stats,profile:resolve(output,'router-test.yaml'),stop:async()=>{for(const socket of sockets)socket.destroy();await Promise.all([new Promise(done=>echo.close(done)),new Promise(done=>socks.close(done))]);udp.close();await writeFile(resolve(output,'fixture-results.json'),JSON.stringify({scope:'Controlled fixture counters; not sufficient alone for router acceptance',stats},null,2)+'\n')}}
}

if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href){
  const arg=name=>{const i=process.argv.indexOf(name);return i<0?undefined:process.argv[i+1]}
  const fixture=await startFixture({bind:arg('--bind')||'127.0.0.1',router:arg('--router')||'192.168.50.1',output:arg('--output')||'output/router-test'})
  console.log(`Fixture ready. Profile: ${fixture.profile}. TCP 21808/21809, UDP 21810; target TEST-NET only.`)
  let stopping=false
  const stop=async()=>{if(stopping)return;stopping=true;await fixture.stop();process.exit(0)}
  process.on('SIGINT',()=>void stop());process.on('SIGTERM',()=>void stop())
}
