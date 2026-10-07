import test from 'node:test';
import assert from 'node:assert/strict';
import worker from './worker.mjs';
test('immutable public releases never list, redirect, mutate or accept credentials in URLs',async()=>{
 const objects=new Map([['releases/v0.5.0/checksums.txt','signed manifest fixture']]);
 const env={RELEASES:{get:async key=>objects.has(key)?{body:objects.get(key),size:objects.get(key).length,httpEtag:'"fixture"'}:null,head:async key=>objects.has(key)?{size:objects.get(key).length}:null}};
 const read=await worker.fetch(new Request('https://downloads.hopsesh.codonic.dev/releases/v0.5.0/checksums.txt'),env);
 assert.equal(read.status,200);assert.match(read.headers.get('Cache-Control'),/immutable/);assert.equal(await read.text(),'signed manifest fixture');
 const head=await worker.fetch(new Request('https://downloads.hopsesh.codonic.dev/releases/v0.5.0/checksums.txt',{method:'HEAD'}),env);assert.equal(head.status,200);assert.equal(await head.text(),'');
 for(const path of ['/latest','/releases/v0.5.0','/releases/v0.5.0/secret.json','/releases/v0.5.0/checksums.txt?token=private','/releases/v0.4.0/checksums.txt'])assert.equal((await worker.fetch(new Request('https://downloads.hopsesh.codonic.dev'+path),env)).status,404);
 assert.equal((await worker.fetch(new Request('https://downloads.hopsesh.codonic.dev/releases/v0.5.0/checksums.txt',{method:'POST'}),env)).status,405);
});

test('publication is authorized, checksum-bound and cannot overwrite an immutable release',async()=>{
 const token='fixture-publication-token-with-32-bytes',objects=new Map();
 const env={PUBLISH_TOKEN:token,RELEASES:{
  put:async(key,body,options)=>{assert.equal(options.onlyIf.get('If-None-Match'),'*');if(objects.has(key))return null;const data=await new Response(body).arrayBuffer();const sum=Buffer.from(await crypto.subtle.digest('SHA-256',data)).toString('hex');if(sum!==options.sha256)throw new Error('checksum');objects.set(key,{size:data.byteLength,customMetadata:options.customMetadata});return{}},
  head:async key=>objects.get(key)
 }};
 const data='signed release fixture',sum=Buffer.from(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(data))).toString('hex');
 const req=(auth=token,hash=sum,body=data)=>new Request('https://downloads.hopsesh.codonic.dev/releases/v0.5.0/checksums.txt',{method:'PUT',headers:{Authorization:'Bearer '+auth,'X-Hopsesh-SHA256':hash,'Content-Length':String(body.length)},body});
 assert.equal((await worker.fetch(req('forged'),env)).status,403);assert.equal(objects.size,0);
 assert.equal((await worker.fetch(req(token,'0'.repeat(64)),env)).status,400);assert.equal(objects.size,0);
 assert.equal((await worker.fetch(req(),env)).status,201);
 assert.equal((await worker.fetch(req(),env)).status,200);
 assert.equal((await worker.fetch(req(token,'1'.repeat(64),'different release'),env)).status,409);
 assert.equal(objects.get('releases/v0.5.0/checksums.txt').customMetadata.sha256,sum);
});
