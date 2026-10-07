// Run locally after signed release preparation. Secrets come only from the
// environment, never arguments, files, URLs, logs or GitHub Actions.
import {createHash,createPublicKey,verify} from 'node:crypto';
import {readFile,lstat,open} from 'node:fs/promises';
import {Readable} from 'node:stream';
import {fileURLToPath} from 'node:url';
import path from 'node:path';

async function boundedRegularFile(file,limit){
 const before=await lstat(file);if(!before.isFile()||before.size<1||before.size>limit)throw new Error('Release metadata is not a bounded regular file');
 const handle=await open(file);
 try{const after=await handle.stat();if(!after.isFile()||after.dev!==before.dev||after.ino!==before.ino||after.size!==before.size)throw new Error('Release metadata changed while opening');const buf=Buffer.alloc(limit+1);let size=0;while(size<buf.length){const {bytesRead}=await handle.read(buf,size,buf.length-size,null);if(!bytesRead)break;size+=bytesRead}if(size>limit)throw new Error('Release metadata exceeds bound');return buf.subarray(0,size)}finally{await handle.close()}
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
 // Verify every selected object before the first network mutation.
 for(const [name,sum]of items){
  const st=await lstat(path.join(directory,name));if(!st.isFile()||st.size<1||st.size>200*1024*1024)throw new Error('Release file is not a bounded regular file');
  const handle=await open(path.join(directory,name));
  try{const hash=createHash('sha256');for await(const chunk of handle.createReadStream({autoClose:false}))hash.update(chunk);if(hash.digest('hex')!==sum)throw new Error('Signed archive checksum failed; nothing published')}finally{await handle.close()}
 }
 for(const [name,sum]of items){
  const handle=await open(path.join(directory,name));
  try{
   const st=await handle.stat();
   const response=await fetcher(url.origin+'/releases/v'+version+'/'+name,{method:'PUT',redirect:'error',headers:{Authorization:'Bearer '+token,'X-Hopsesh-SHA256':sum,'Content-Length':String(st.size)},body:Readable.toWeb(handle.createReadStream({autoClose:false})),duplex:'half',signal:AbortSignal.timeout(120000)});
   if(![200,201].includes(response.status))throw new Error('Immutable publication refused with HTTP '+response.status);
  }finally{await handle.close()}
 }
 return [...items.keys()];
}

if(process.argv[1]===fileURLToPath(import.meta.url)){
 try{
  const [version,directory]=process.argv.slice(2);
  if(!version||!directory)throw new Error('Usage: node publish.mjs <0.5-version> <signed-release-directory>');
  const files=await publish(version,directory,process.env.HOPSESH_DOWNLOADS_ORIGIN||'https://downloads.hopsesh.codonic.dev',process.env.HOPSESH_DOWNLOADS_PUBLISH_TOKEN);
  process.stdout.write(`Published ${files.length} verified immutable objects for ${version}\n`);
 }catch(err){process.stderr.write(`Publication stopped: ${err.message}\n`);process.exitCode=1}
}
