import { readFile, writeFile, mkdir, cp, readdir, stat, access, copyFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { gunzipSync, deflateSync } from 'node:zlib'
import { resolve, dirname, join, relative } from 'node:path'
import { spawnSync } from 'node:child_process'
import process from 'node:process'

const root = resolve('.')
const version = (await readFile('package/cwclash/version', 'utf8')).trim()
const bootstrap = process.argv.includes('--bootstrap')
const usb = process.argv.includes('--usb')
if (usb && bootstrap) throw Error('USB delivery is a complete package; omit --bootstrap')
const storageMode = usb ? 'usb-jffs' : 'native'
if (!/^\d+\.\d+\.\d+$/.test(version)) throw Error('Invalid package version')
const portableGo = resolve('.tools/go/bin', process.platform === 'win32' ? 'go.exe' : 'go')
const go = process.env.CWCLASH_GO || portableGo
await access(go).catch(() => { throw Error('Go toolchain missing. Set CWCLASH_GO to a Go executable.') })
await mkdir('.cache/tmp', { recursive: true }); await mkdir('dist', { recursive: true }); await mkdir('build', { recursive: true })
const goenv = { ...process.env, GOCACHE: resolve('.cache/go-build'), GOMODCACHE: resolve('.tools/gomod'), GOTMPDIR: resolve('.cache/tmp') }
const run = (exe, args, extra = {}) => {
  const result = spawnSync(exe, args, { cwd: root, stdio: 'inherit', windowsHide: true, ...extra })
  if (result.error) throw result.error
  if (result.status !== 0) throw Error(`${exe} failed (${result.status})`)
}
run(process.execPath, [join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), 'run', 'build'], { cwd: resolve('web') })
run(go, ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version} -X main.storageMode=${storageMode}`, '-o', 'build/cwclash-arm64', './cmd/cwclash'], { env: { ...goenv, GOOS: 'linux', GOARCH: 'arm64', CGO_ENABLED: '0' } })

const lock = JSON.parse(await readFile('dependencies/mihomo.lock.json', 'utf8'))
const asset = lock.assets.find(a => a.name.includes('linux-arm64'))
const compressed = await readFile(resolve('.tools/mihomo', asset.name))
const digest = data => createHash('sha256').update(data).digest('hex')
if (digest(compressed) !== asset.sha256 || compressed.length !== asset.size) throw Error('Locked mihomo archive checksum mismatch')
const kernel = gunzipSync(compressed)
if (kernel.subarray(0,4).toString('hex') !== '7f454c46' || kernel[4] !== 2 || kernel.readUInt16LE(18) !== 183) throw Error('Kernel is not ELF64/AArch64')
const sha256 = digest(kernel), file = `core-${sha256.slice(0,24)}`
const stage = resolve(`build/stage-${Date.now()}`), pkg = join(stage, 'cwclash')
await cp('package/cwclash', pkg, { recursive: true })
// Shell command substitution removes LF, not CR. Canonicalize the version
// independently of Windows checkout/editor line endings before hashing it.
await writeFile(join(pkg, 'version'), version + '\n')
await writeFile(join(pkg,'storage-mode'),storageMode+'\n')
const aspPath = join(pkg, 'webs/Module_cwclash.asp')
await writeFile(aspPath, (await readFile(aspPath, 'utf8')).replaceAll('__CW_VERSION__', version))
await mkdir(join(pkg, 'bin'), { recursive: true }); await mkdir(join(pkg, 'cores'), { recursive: true }); await mkdir(join(pkg, 'licenses'), { recursive: true })
const manager = await readFile('build/cwclash-arm64')
const managerStage = resolve(`build/manager-payload-${Date.now()}`)
await mkdir(managerStage, {recursive:true})
await writeFile(join(managerStage, 'cwclash.next'), manager)
const managerArchivePath = join(pkg, 'bin/manager.tar.gz')
run(go, ['run','./cmd/cwpack',managerStage,managerArchivePath], {env:goenv})
await writeFile(join(pkg, 'bin/manager-archive.sha256'), digest(await readFile(managerArchivePath))+'\n')
await writeFile(join(pkg, 'bin/manager.sha256'), digest(manager)+'\n')
if (!bootstrap) {
 if (usb) {
  await writeFile(join(pkg,'cores',file),kernel)
  await writeFile(join(pkg,'bundled-kernel.json'),JSON.stringify({version:lock.version,file,sha256},null,2)+'\n')
 } else {
  const raw = resolve('build',`core-unpacked-${sha256.slice(0,24)}`), packed = resolve('build',`core-packed-${sha256.slice(0,24)}.xz`)
  await writeFile(raw, kernel)
  run(go, ['run','./cmd/cwxz',raw,packed], {env:goenv})
  const coreArchive = await readFile(packed), packedName = `corexz-${sha256.slice(0,24)}`
  await writeFile(join(pkg, 'cores', packedName), coreArchive)
  await writeFile(join(pkg, 'bundled-kernel.json'), JSON.stringify({ version: lock.version, file:packedName, sha256, compression:'xz', archiveSHA256:digest(coreArchive) }, null, 2)+'\n')
 }
}
await copyFile('LICENSE', join(pkg, 'LICENSE')); await copyFile('THIRD_PARTY.md', join(pkg, 'THIRD_PARTY.md'))
for (const name of ['app.js', 'style.css']) await copyFile(join('web/dist', name), join(pkg, 'res/cwclash', name))

// Small code-drawn PNG for software centers that require icon-NAME.png.
const width=128, pixels=Buffer.alloc((width*4+1)*width)
for(let y=0;y<width;y++){pixels[y*(width*4+1)]=0;for(let x=0;x<width;x++){const offset=y*(width*4+1)+1+x*4;const edgeX=Math.max(0,24-x,x-103),edgeY=Math.max(0,24-y,y-103);const visible=edgeX*edgeX+edgeY*edgeY<24*24;pixels.set([33,62,50,visible?255:0],offset)}}
const segment=(x1,y1,x2,y2)=>{for(let y=0;y<width;y++)for(let x=0;x<width;x++){const dx=x2-x1,dy=y2-y1,t=Math.max(0,Math.min(1,((x-x1)*dx+(y-y1)*dy)/(dx*dx+dy*dy||1)));if((x-x1-t*dx)**2+(y-y1-t*dy)**2<=13){const offset=y*(width*4+1)+1+x*4;pixels.set([163,231,196,255],offset)}}}
for(const s of [[24,101,24,46],[24,46,39,46],[39,46,39,25],[39,25,58,25],[58,25,58,46],[58,46,71,46],[71,46,71,25],[71,25,90,25],[90,25,90,46],[90,46,104,46],[104,46,104,101],[24,68,104,68],[47,68,47,101],[80,68,80,101],[24,101,104,101]])segment(...s)
function crc32(buf){let crc=0xffffffff;for(const b of buf){crc^=b;for(let i=0;i<8;i++)crc=(crc>>>1)^((crc&1)?0xedb88320:0)}return (crc^0xffffffff)>>>0}
function chunk(type,data){const name=Buffer.from(type),length=Buffer.alloc(4),crc=Buffer.alloc(4);length.writeUInt32BE(data.length);crc.writeUInt32BE(crc32(Buffer.concat([name,data])));return Buffer.concat([length,name,data,crc])}
const ihdr=Buffer.alloc(13);ihdr.writeUInt32BE(width);ihdr.writeUInt32BE(width,4);ihdr[8]=8;ihdr[9]=6
await writeFile(join(pkg,'res/icon-cwclash.png'),Buffer.concat([Buffer.from('89504e470d0a1a0a','hex'),chunk('IHDR',ihdr),chunk('IDAT',deflateSync(pixels)),chunk('IEND',Buffer.alloc(0))]))

for(const [source,name] of [[join('.tools/go','LICENSE'),'Go-BSD-3-Clause.txt'],[join('web/node_modules/vue','LICENSE'),'Vue-MIT.txt'],[join('web/node_modules/vite','LICENSE.md'),'Vite-LICENSE.txt'],[join('.tools/gomod/gopkg.in/yaml.v3@v3.0.1','LICENSE'),'yaml-v3-LICENSE.txt'],[join('.tools/gomod/github.com/ulikunitz/xz@v0.5.15','LICENSE'),'xz-BSD-3-Clause.txt']]){
  try{await copyFile(source,join(pkg,'licenses',name))}catch{throw Error(`Missing license: ${source}`)}
}
async function files(dir){const out=[];for(const item of await readdir(dir,{withFileTypes:true})){const path=join(dir,item.name);if(item.isDirectory())out.push(...await files(path));else if(item.isFile())out.push(path);else throw Error('Unexpected symlink in package')}return out.sort()}
const paths = await files(pkg)
const checks=[];let unpacked=0
for(const path of paths){const data=await readFile(path);unpacked+=data.length;checks.push(`${digest(data)}  ${relative(pkg,path).replaceAll('\\','/')}`)}
await writeFile(join(pkg,'CHECKSUMS.sha256'),checks.join('\n')+'\n')
const base=`cwclash_hnd_v8_${usb?'usb_':bootstrap?'bootstrap_':''}${version}`
const output=resolve('dist',`${base}.tar.gz`)
run(go,['run','./cmd/cwpack',stage,output],{env:goenv})
const artifact=await readFile(output)
await writeFile(resolve('dist',`${base}.sha256`),`${digest(artifact)}  ${output.split(/[\\/]/).at(-1)}\n`)
await writeFile(resolve('dist',`${base}.manifest.json`),JSON.stringify({version,platform:'hnd_v8 / Linux ARM64',model:'GT-AX6000',storageMode,requires:usb?'USB2JFFS on writable USB EXT4; 128 MiB free after install':'Native JFFS (ubifs/jffs2) or ext3/ext4; 4 MiB free after install',bundledKernel:!bootstrap,kernel:bootstrap?null:lock.version,kernelBinarySHA256:bootstrap?null:sha256,kernelStorage:usb?'raw':'xz',packageSHA256:digest(artifact),packageBytes:artifact.length,unpackedBytes:unpacked,files:paths.length},null,2)+'\n')
console.log(`Built ${output}\nPackage ${artifact.length} bytes; payload ${unpacked} bytes; SHA256 ${digest(artifact)}`)
