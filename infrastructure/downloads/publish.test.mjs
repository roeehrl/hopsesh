import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {mkdtemp,writeFile,rm,symlink,rename} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {publish} from './publish.mjs';

test('publisher validates all signed objects before mutation and retries immutable objects safely',async()=>{
 const {publicKey,privateKey}=generateKeyPairSync('ec',{namedCurve:'prime256v1'});
 for(const scenario of ['valid','bad-signature','bad-archive']){
  const dir=await mkdtemp(path.join(os.tmpdir(),'hopsesh-publication-'));
  try{
   const archive=Buffer.from('disposable signed archive fixture');
   const manifest=Buffer.from(createHash('sha256').update(archive).digest('hex')+'  hopsesh_0.5.0_linux_amd64.tar.gz\n');
   const signature=sign('sha256',manifest,privateKey);
   if(scenario==='bad-signature')signature[0]^=1;
   await writeFile(path.join(dir,'checksums.txt'),manifest);
   await writeFile(path.join(dir,'checksums.txt.sig'),signature);
   await writeFile(path.join(dir,'hopsesh_0.5.0_linux_amd64.tar.gz'),scenario==='bad-archive'?Buffer.from('tampered'):archive);
   let calls=0;
   const fetcher=async(url,options)=>{
    calls++;assert.match(url,/^https:\/\/downloads\.example\/releases\/v0\.5\.0\//);
    assert.equal(options.redirect,'error');assert.equal(options.method,'PUT');
    const body=Buffer.from(await new Response(options.body).arrayBuffer());
    assert.equal(createHash('sha256').update(body).digest('hex'),options.headers['X-Hopsesh-SHA256']);
    return new Response(null,{status:calls>3?200:201});
   };
   const run=()=>publish('0.5.0',dir,'https://downloads.example','fixture-local-publication-token-32-bytes',fetcher,publicKey.export({type:'spki',format:'pem'}));
   if(scenario==='valid'){assert.equal((await run()).length,3);assert.equal((await run()).length,3);assert.equal(calls,6)}
   else {await assert.rejects(run);assert.equal(calls,0,'invalid release made a network mutation')}
  }finally{await rm(dir,{recursive:true,force:true})}
 }
});

test('publication keeps verified bytes when source paths or opened source contents change between uploads',async()=>{
 const {publicKey,privateKey}=generateKeyPairSync('ec',{namedCurve:'prime256v1'});
 for(const scenario of ['replace','in-place','symlink']){
  const dir=await mkdtemp(path.join(os.tmpdir(),'hopsesh-publication-race-'));
  try{
   const names=['hopsesh_0.5.0_linux_amd64.tar.gz','hopsesh_0.5.0_darwin_arm64.tar.gz'];
   const payloads=names.map(name=>Buffer.from('signed payload '+name));
   const manifest=Buffer.from(names.map((name,i)=>createHash('sha256').update(payloads[i]).digest('hex')+'  '+name).join('\n')+'\n');
   await writeFile(path.join(dir,'checksums.txt'),manifest);
   await writeFile(path.join(dir,'checksums.txt.sig'),sign('sha256',manifest,privateKey));
   for(let i=0;i<names.length;i++)await writeFile(path.join(dir,names[i]),payloads[i]);
   let calls=0;
   const fetcher=async(url,options)=>{
    const bytes=Buffer.from(await new Response(options.body).arrayBuffer());
    assert.equal(createHash('sha256').update(bytes).digest('hex'),options.headers['X-Hopsesh-SHA256']);
    assert.equal(bytes.length,Number(options.headers['Content-Length']));
    if(calls<2)assert.deepEqual(bytes,payloads[calls]);
    if(calls++===0){
     const target=path.join(dir,names[1]);
     if(scenario==='replace'){
      await writeFile(path.join(dir,'replacement'),'different bytes');
      await rename(path.join(dir,'replacement'),target);
     }else if(scenario==='in-place')await writeFile(target,'different bytes');
     else{
      await rm(target);await writeFile(path.join(dir,'outside'),'unreviewed private bytes');
      await symlink(path.join(dir,'outside'),target);
     }
     await writeFile(path.join(dir,'checksums.txt'),'changed metadata');
     await writeFile(path.join(dir,'checksums.txt.sig'),'changed signature');
    }
    return new Response(null,{status:201});
   };
   await publish('0.5.0',dir,'https://downloads.example','fixture-local-publication-token-32-bytes',fetcher,publicKey.export({type:'spki',format:'pem'}));
   assert.equal(calls,4);
  }finally{await rm(dir,{recursive:true,force:true})}
 }
});

test('publisher refuses oversized or linked release metadata before network mutations',async()=>{
 const {publicKey}=generateKeyPairSync('ec',{namedCurve:'prime256v1'});
 const dir=await mkdtemp(path.join(os.tmpdir(),'hopsesh-publication-metadata-'));
 let calls=0;const fetcher=async()=>{calls++;return new Response(null,{status:201})};
 const run=()=>publish('0.5.0',dir,'https://downloads.example','fixture-local-publication-token-32-bytes',fetcher,publicKey.export({type:'spki',format:'pem'}));
 try{
  await writeFile(path.join(dir,'checksums.txt'),Buffer.alloc((1<<20)+1));await writeFile(path.join(dir,'checksums.txt.sig'),'signature');
  await assert.rejects(run,/bounded regular file/);
  await rm(path.join(dir,'checksums.txt'));await writeFile(path.join(dir,'target'),'manifest');
  try{await symlink(path.join(dir,'target'),path.join(dir,'checksums.txt'))}catch(e){if(e.code==='EPERM')return;throw e}
  await assert.rejects(run,/bounded regular file/);assert.equal(calls,0);
 }finally{await rm(dir,{recursive:true,force:true})}
});
