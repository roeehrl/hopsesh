import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp,symlink,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {operatorStats} from './operator.mjs';
const input={origin:'https://relay.hopsesh.codonic.dev',space:'approved-space-0001',token:'private-operator-credential-at-least-32'};
const counts={devices:2,messages:1,ciphertextBytes:500,pendingDeletions:0,tombstones:1,day:1,admittedFrames:1,admittedBytes:500,resetAt:172800,paused:false,limits:{dailyFrames:4096,dailyBytes:268435456}};
test('operator CLI validates stdin through directory aliases and imports without running',async()=>{
 const dir=await mkdtemp(path.join(os.tmpdir(),'hopsesh-operator-entry-'));
 try{
  const source=fileURLToPath(new URL('.',import.meta.url)),alias=path.join(dir,'operator-source');
  await symlink(source,alias,process.platform==='win32'?'junction':'dir');
  for(const entry of [path.join(source,'operator.mjs'),path.join(alias,'operator.mjs')]){
   const result=spawnSync(process.execPath,[entry],{input:'{}',encoding:'utf8',timeout:10000});
   assert.ifError(result.error);
   assert.equal(result.status,1,entry+' silently skipped validation');
   assert.match(result.stderr,/operator status failed/);
   assert.equal(result.stdout,'');
   const imported=spawnSync(process.execPath,['--input-type=module','-e',`await import(${JSON.stringify(pathToFileURL(entry).href)})`],{input:'{}',encoding:'utf8',timeout:10000});
   assert.ifError(imported.error);
   assert.equal(imported.status,0,imported.stderr);
   assert.equal(imported.stderr,'');assert.equal(imported.stdout,'');
  }
 }finally{await rm(dir,{recursive:true,force:true})}
});
test('operator client fixes route, refuses redirects and emits only counts',async()=>{
 const out=await operatorStats(input,async(url,options)=>{assert.equal(url.href,input.origin+'/v1/operator/stats');assert.equal(options.redirect,'error');assert.equal(options.headers.Authorization,'Bearer '+input.token);return Response.json({...counts,email:'must not emit',token:'must not emit'})});
 assert.deepEqual(out,counts);
});
test('operator client refuses alternate origins, malformed counters and oversized responses',async()=>{
 for(const origin of ['http://relay.test','https://relay.test/query?secret=1','https://user:password@relay.test','https://relay.test/#secret'])await assert.rejects(operatorStats({...input,origin},()=>{throw Error('must not connect')}));
 await assert.rejects(operatorStats(input,async()=>Response.json({...counts,messages:'1'})));
 await assert.rejects(operatorStats(input,async()=>new Response('x'.repeat(9000))));
});
