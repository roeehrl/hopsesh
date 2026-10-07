// Run locally after signed release preparation. Secrets come only from the
// environment, never arguments, files, URLs, logs or GitHub Actions.
import {createHash,createPublicKey,verify} from 'node:crypto';
import {readFile,lstat,open,mkdtemp,rm} from 'node:fs/promises';
import {constants} from 'node:fs';
import os from 'node:os';
import {Readable} from 'node:stream';
import {fileURLToPath} from 'node:url';
import path from 'node:path';

async function boundedRegularFile(file,limit){
 const before=await lstat(file);if(!before.isFile()||before.size<1||before.size>limit)throw new Error('Release metadata is not a bounded regular file');
 const handle=await open(file,constants.O_RDONLY|(constants.O_NOFOLLOW||0));
 try{const after=await handle.stat();if(!sameFile(before,after))throw new Error('Release metadata changed while opening');const buf=Buffer.alloc(limit+1);let size=0;while(size<buf.length){const {bytesRead}=await handle.read(buf,size,buf.length-size,null);if(!bytesRead)break;size+=bytesRead}if(size>limit||size!==before.size||!sameFile(after,await handle.stat()))throw new Error('Release metadata changed or exceeds bound');return buf.subarray(0,size)}finally{await handle.close()}
}

function sameFile(a,b){return b.isFile()&&a.dev===b.dev&&a.ino===b.ino&&a.size===b.size&&a.mtimeMs===b.mtimeMs&&a.ctimeMs===b.ctimeMs}

// Hash and freeze through the same opened object. Upload never reopens a source
// path: even an in-place edit after verification cannot change publication bytes.
async function freezeArchive(source,destination,sum){
 const before=await lstat(source);
 if(!before.isFile()||before.size<1||before.size>200*1024*1024)throw new Error('Release file is not a bounded regular file');
 const input=await open(source,constants.O_RDONLY|(constants.O_NOFOLLOW||0));
 try{
  if(!sameFile(before,await input.stat()))throw new Error('Release file changed while opening');
  const hash=createHash('sha256');let size=0;
  for await(const chunk of input.createReadStream({autoClose:false})){
   size+=chunk.length;if(size>before.size)throw new Error('Release file grew during verification');
   hash.update(chunk);await destination.writeFile(chunk);
  }
  if(size!==before.size||!sameFile(before,await input.stat())||hash.digest('hex')!==sum)throw new Error('Signed archive checksum failed or source changed; nothing published');
  return size;
 }finally{await input.close()}
}

export async function publish(version,directory,origin,token,fetcher=fetch,publicKey=null){
 if(!/^0\.5\.\d+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?$/.test(version))throw new Error('Explicit immutable 0.5 release version required');
 const url=new URL(origin);if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash||!['','/'].includes(url.pathname))throw new Error('Verified HTTPS origin required');
 if(!token||token.length<32||/[\r\n]/.test(token))throw new Error('Local publication credential required');
 // Tests inject an ephemeral key; the CLI exposes no alternate-key setting.
 const key=publicKey||await readFile(new URL('../../packaging/release-key.pub',import.meta.url));
 const manifest=await boundedRegularFile(path.join(directory,'checksums.txt'),1<<20),signature=await boundedRegularFile(path.join(directory,'checksums.txt.sig'),8192);
 if(!verify('sha256',manifest,createPublicKey(key),signature))throw new Error('Release signature failed; nothing published');
 const items=new Map(),seen=new Set();
 for(const line of manifest.toString('utf8').trim().split('\n')){
  const match=/^([a-f0-9]{64})\s+\*?([^/\\\r\n]+)$/.exec(line);if(!match||seen.has(match[2]))throw new Error('Invalid or duplicate signed checksum');seen.add(match[2]);
  // The cloud CDN distributes CLI archives only. Other release assets remain
  // on GitHub; their lines stay in the unchanged signed manifest.
  if(new RegExp('^hopsesh_'+version.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')+'_(?:linux|darwin|windows)_(?:amd64|arm64)\\.(?:tar\\.gz|zip)$').test(match[2]))items.set(match[2],match[1]);
 }
 if(!items.size)throw new Error('No signed CLI archives found');
 items.set('checksums.txt',createHash('sha256').update(manifest).digest('hex'));
 items.set('checksums.txt.sig',createHash('sha256').update(signature).digest('hex'));
 // Freeze every selected object before the first network mutation. The private
 // staging directory and open handles belong only to this publication attempt.
 const staging=await mkdtemp(path.join(os.tmpdir(),'hopsesh-publication-frozen-'));
 const frozen=[];
 try{
  for(const [name,sum]of items){
   const handle=await open(path.join(staging,String(frozen.length)),'wx+',0o600);
   const item={name,sum,handle,size:0};frozen.push(item);
   if(name==='checksums.txt'||name==='checksums.txt.sig'){
    const bytes=name==='checksums.txt'?manifest:signature;
    await handle.writeFile(bytes);item.size=bytes.length;
   }else item.size=await freezeArchive(path.join(directory,name),handle,sum);
  }
  for(const {name,sum,handle,size}of frozen){
   const response=await fetcher(url.origin+'/releases/v'+version+'/'+name,{method:'PUT',redirect:'error',headers:{Authorization:'Bearer '+token,'X-Hopsesh-SHA256':sum,'Content-Length':String(size)},body:Readable.toWeb(handle.createReadStream({autoClose:false,start:0})),duplex:'half',signal:AbortSignal.timeout(120000)});
   if(![200,201].includes(response.status))throw new Error('Immutable publication refused with HTTP '+response.status);
  }
  return [...items.keys()];
 }finally{
  await Promise.allSettled(frozen.map(({handle})=>handle.close()));
  await rm(staging,{recursive:true,force:true});
 }
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
 try{
  const [version,directory]=process.argv.slice(2);
  if(!version||!directory)throw new Error('Usage: node publish.mjs <0.5-version> <signed-release-directory>');
  const files=await publish(version,directory,process.env.HOPSESH_DOWNLOADS_ORIGIN||'https://downloads.hopsesh.codonic.dev',process.env.HOPSESH_DOWNLOADS_PUBLISH_TOKEN);
  process.stdout.write(`Published ${files.length} verified immutable objects for ${version}\n`);
 }catch(err){process.stderr.write(`Publication stopped: ${err.message}\n`);process.exitCode=1}
}
