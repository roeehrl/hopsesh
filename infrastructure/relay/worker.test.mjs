import test from 'node:test';
import assert from 'node:assert/strict';
import worker, {createHandler, Mailbox, LIMITS, maintain} from './worker.mjs';
class Storage {
 constructor(){this.values=new Map();this.alarm=null;this.tail=Promise.resolve()}
 async get(k){return structuredClone(this.values.get(k))}
 async put(k,v){this.values.set(k,structuredClone(v))}
 async delete(k){return this.values.delete(k)}
 async list({prefix='',limit=Infinity}={}){return new Map([...this.values].filter(([k])=>k.startsWith(prefix)).sort(([a],[b])=>a.localeCompare(b)).slice(0,limit))}
 async setAlarm(t){this.alarm=t}
 async getAlarm(){return this.alarm}
 async transaction(fn){let release;const before=this.tail;this.tail=new Promise(r=>release=r);await before;const tx=new Storage();tx.values=structuredClone(this.values);try{const result=await fn(tx);this.values=tx.values;return result}finally{release()}}
}
class Bucket {constructor(){this.values=new Map()}async put(k,v){this.values.set(k,v)}async get(k){const v=this.values.get(k);return v===undefined?null:{text:async()=>v}}async delete(k){this.values.delete(k)}}
async function fixture(events,policy){
 const storage=new Storage(),bucket=new Bucket(),space='space-12345678901',admin='test-operator-secret';let clock=Date.now();
 const handle=createHandler(storage,bucket,admin,space,()=>clock,events,policy);
 const invoke=(path,method='GET',token='',body)=>handle(new Request('https://relay.test'+path,{method,headers:{Authorization:'Bearer '+token,'X-Hopsesh-Space':space},body:body===undefined?undefined:JSON.stringify(body)}));
 const enroll=async device=>{const r=await invoke('/v1/enrollment/register','POST',admin,{device,ttl:3600});assert.equal(r.status,201);return(await r.json()).token};
 const a='endpoint-A-123456',b='endpoint-B-123456',ta=await enroll(a),tb=await enroll(b);
 const envelope=(id='message-123456789')=>({kind:"request",protocol:1,id,space,from:a,to:b,operation:'operation-123456',created:Math.floor(clock/1000),expires:Math.floor(clock/1000)+300,ciphertext:'opaque-encrypted-payload',signature:'x'.repeat(88)});
 return{storage,bucket,invoke,ta,tb,a,b,envelope,advance:ms=>clock+=ms};
}
test('serialized daily traffic ceiling survives acknowledgment, duplicate retry and midnight rollover',async()=>{
 const f=await fixture(undefined,{dailyFrames:2,dailyBytes:100000});
 const statuses=await Promise.all(Array.from({length:12},(_,i)=>f.invoke('/v1/messages','POST',f.ta,f.envelope('budget-message-'+String(i).padStart(4,'0'))).then(r=>r.status)));
 assert.equal(statuses.filter(s=>s===201).length,2);assert.equal(statuses.filter(s=>s===429).length,10);
 assert.equal(f.bucket.values.size,2);
 const batch=await(await f.invoke('/v1/messages','GET',f.tb)).json();
 await f.invoke('/v1/ack','POST',f.tb,{cursor:batch.cursor});
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('new-budget-message'))).status,429,'ack must not refund daily admission');
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('budget-message-0000'))).status,200,'committed retry must not consume another reservation');
 const stored=await f.storage.get('day-budget');assert.equal(stored.frames,2);
 // Move the existing counter to yesterday: the next transaction rolls it over
 // without an idle timer or extra maintenance wakeup.
 await f.storage.put('day-budget',{...stored,day:stored.day-1});
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('rollover-message-001'))).status,201);
 assert.equal((await f.storage.get('day-budget')).frames,1);
});
test('byte budget blocks before R2 publication and operator metrics disclose counts only',async()=>{
 const f=await fixture(undefined,{dailyBytes:1,dailyFrames:99999999});
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope())).status,429);assert.equal(f.bucket.values.size,0);
 assert.equal((await f.invoke('/v1/operator/stats','GET',f.ta)).status,403);
 const stats=await(await f.invoke('/v1/operator/stats','GET','test-operator-secret')).json();
 assert.deepEqual(Object.keys(stats).sort(),['admittedBytes','admittedFrames','ciphertextBytes','day','devices','limits','messages','paused','pendingDeletions','resetAt','tombstones'].sort());
 assert.equal(stats.devices,2);assert.equal(stats.admittedFrames,0);assert.equal(stats.limits.dailyFrames,LIMITS.dailyFrames);
 assert.equal(JSON.stringify(stats).includes(f.a),false);assert.equal(JSON.stringify(stats).includes(f.ta),false);
});
test('operator pause rejects new traffic while allowing drain and metadata inspection',async()=>{
 const policy={paused:false},f=await fixture(undefined,policy);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope())).status,201);
 policy.paused=true;
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('paused-message-001'))).status,503);
 assert.equal((await f.invoke('/v1/enrollment/register','POST','test-operator-secret',{device:'paused-device-0001',ttl:300})).status,503);
 const batch=await(await f.invoke('/v1/messages','GET',f.tb)).json();assert.equal(batch.messages.length,1);
 assert.equal((await f.invoke('/v1/ack','POST',f.tb,{cursor:batch.cursor})).status,200);assert.equal(f.bucket.values.size,0);
 const stats=await(await f.invoke('/v1/operator/stats','GET','test-operator-secret')).json();assert.equal(stats.paused,true);assert.equal(stats.admittedFrames,1);
});
test('operator pause prevents paid allocation before a new mailbox or admission',async()=>{
 let allocations=0;const env={RELAY_PAUSED:'1',MAILBOX:{idFromName(){allocations++;throw Error('must not allocate')}},AUTHORIZATION:{idFromName(){allocations++;throw Error('must not allocate')}}};
 for(const path of ['/v1/messages','/v1/cloud/claim','/v1/device/code','/v1/enrollment/register'])assert.equal((await worker.fetch(new Request('https://relay.test'+path,{method:'POST',body:'{}'}),env)).status,503);
 assert.equal(allocations,0);
});
test('handler deduplicates encrypted delivery; GET does not acknowledge',async()=>{
 const f=await fixture(),e=f.envelope();assert.equal((await f.invoke('/v1/messages','POST',f.ta,e)).status,201);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,e)).status,200);assert.equal(f.bucket.values.size,1);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,{...e,ciphertext:'changed'})).status,409);
 assert.equal((await f.invoke('/v1/messages','POST',f.tb,e)).status,400);
 assert.equal((await f.invoke('/v1/messages','GET','invalid')).status,403);
 const batch=await(await f.invoke('/v1/messages','GET',f.tb)).json();assert.deepEqual(batch.messages[0].envelope,e);
 assert.equal(f.bucket.values.size,1);assert.equal((await f.invoke('/v1/ack','POST',f.tb,{cursor:batch.cursor})).status,200);
 assert.equal(f.bucket.values.size,0);assert.equal((await f.invoke('/v1/messages','POST',f.ta,e)).status,200);
 assert.equal(f.bucket.values.size,0,'ack must retain idempotency tombstone');
});
test('logical expiry denies retrieval before physical deletion',async()=>{
 const f=await fixture();await f.invoke('/v1/messages','POST',f.ta,f.envelope());f.advance(301000);
 const batch=await(await f.invoke('/v1/messages','GET',f.tb)).json();assert.equal(batch.messages.length,0);assert.equal(f.bucket.values.size,1);
});
test('revoked credentials and recipient cannot route; scoped token cannot enroll',async()=>{
 const f=await fixture();assert.equal((await f.invoke('/v1/enrollment/register','POST',f.ta,{device:'another-123456789',ttl:3600})).status,403);
 assert.equal((await f.invoke('/v1/enrollment/revoke','POST',f.tb,{})).status,200);
 assert.equal((await f.invoke('/v1/messages','GET',f.tb)).status,403);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope())).status,403);
});
test('concurrent publication obeys quota and orders messages uniquely',async()=>{
 const f=await fixture();await f.storage.put('quota:'+f.b,{bytes:LIMITS.bytes-100,count:0});
 let results=await Promise.all(Array.from({length:10},(_,i)=>f.invoke('/v1/messages','POST',f.ta,f.envelope('message-number-'+String(i).padStart(3,'0')))));
 assert.ok(results.every(r=>r.status===429));assert.equal(f.bucket.values.size,0);
 await f.storage.put('quota:'+f.b,{bytes:0,count:0});
 results=await Promise.all(Array.from({length:10},(_,i)=>f.invoke('/v1/messages','POST',f.ta,f.envelope('message-number-'+String(i).padStart(3,'0')))));
 const sequences=await Promise.all(results.map(r=>r.json().then(v=>v.sequence)));assert.equal(new Set(sequences).size,10);
});
test('method, namespace and cursor restrictions refuse smuggled writes',async()=>{
 const f=await fixture();assert.equal((await f.invoke('/v1/ack','GET',f.tb)).status,405);
 assert.equal((await f.invoke('/v1/messages?cursor=-1','GET',f.tb)).status,400);
 assert.equal((await f.invoke('/v1/ack','POST',f.tb,{cursor:100})).status,400);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,{...f.envelope(),space:'another-space-123'})).status,400);
 assert.equal(f.bucket.values.size,0);
});
test('physical cleanup removes ciphertext and retains receipt tombstone',async()=>{
 const f=await fixture();await f.invoke('/v1/messages','POST',f.ta,f.envelope());
 for(const[k,m]of await f.storage.list({prefix:'message:'}))await f.storage.put(k,{...m,expires:1});
 await new Mailbox({storage:f.storage},{CIPHERTEXT:f.bucket}).alarm();assert.equal(f.bucket.values.size,0);
 assert.equal((await f.storage.list({prefix:'message:'})).size,0);assert.equal((await f.storage.list({prefix:'id:'})).size,1);
});

test('unauthenticated namespace cannot allocate a paid Durable Object',async()=>{
 let allocated=0;
 const env={ENROLLMENT_ADMIN:'operator-secret-with-at-least-32-bytes',MAILBOX:{idFromName(){allocated++;return 'object'},get(){throw new Error('must not allocate')}}};
 const req=new Request('https://relay.test/v1/messages',{headers:{'X-Hopsesh-Space':'invented-space-123','Authorization':'Bearer forged'}});
 assert.equal((await worker.fetch(req,env)).status,403);assert.equal(allocated,0);
 const enrollment=new Request('https://relay.test/v1/enrollment/register',{method:'POST',headers:{'X-Hopsesh-Space':'invented-space-123','Authorization':'Bearer forged'},body:'{}'});
 assert.equal((await worker.fetch(enrollment,env)).status,403);assert.equal(allocated,0);
});

test('R2 deletion failure cannot leave a missing object at the head of a mailbox',async()=>{
 const f=await fixture();
 await f.invoke('/v1/messages','POST',f.ta,f.envelope());
 const batch=await(await f.invoke('/v1/messages','GET',f.tb)).json();
 const remove=f.bucket.delete.bind(f.bucket);let fail=true;
 f.bucket.delete=async key=>{await remove(key);if(fail)throw new Error('simulated R2 partial deletion')};
 assert.equal((await f.invoke('/v1/ack','POST',f.tb,{cursor:batch.cursor})).status,503);
 assert.equal((await f.storage.list({prefix:'message:'})).size,0);
 assert.equal((await f.storage.list({prefix:'delete:'})).size,1);
 fail=false;
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('message-next-12345'))).status,201);
 const next=await(await f.invoke('/v1/messages','GET',f.tb)).json();assert.equal(next.messages.length,1);
 await maintain(f.storage,f.bucket,Math.floor(Date.now()/1000));
 assert.equal((await f.storage.list({prefix:'delete:'})).size,0);
 assert.equal((await f.storage.get('quota:'+f.b)).count,1);
});

test('expired dedupe receipts and routing devices release quotas without accepting an expired envelope',async()=>{
 const f=await fixture(),e=f.envelope();await f.invoke('/v1/messages','POST',f.ta,e);
 await maintain(f.storage,f.bucket,e.expires+1);
 assert.equal((await f.storage.list({prefix:'id:'})).size,0);assert.equal(await f.storage.get('id-count'),0);
 assert.equal((await f.storage.list({prefix:'device:'})).size,2);
 f.advance(301000);assert.equal((await f.invoke('/v1/messages','POST',f.ta,e)).status,400);
 await maintain(f.storage,f.bucket,e.created+3601);
 assert.equal((await f.storage.list({prefix:'device:'})).size,0);assert.equal((await f.storage.list({prefix:'token:'})).size,0);assert.equal(await f.storage.get('device-count'),0);
});

test('notifications follow durable publication and never carry encrypted or native content',async()=>{
 let f;const hints=[];
 f=await fixture({changed:async device=>{
  assert.equal((await f.storage.list({prefix:'message:'+device+':'})).size,1);
  assert.equal(f.bucket.values.size,1);
  hints.push(device);throw new Error('broken notification socket');
 },refresh:async()=>{},subscribe:()=>new Response(null,{status:204})});
 const envelope=f.envelope();
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,envelope)).status,201,'a failed hint cannot undo successful publication');
 assert.deepEqual(hints,[f.b]);
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,envelope)).status,200);
 assert.equal(hints.length,1,'deduplication does not wake the receiver again');
 f.bucket.put=async()=>{throw new Error('storage')};
 assert.equal((await f.invoke('/v1/messages','POST',f.ta,f.envelope('another-message-123'))).status,503);
 assert.equal(hints.length,1,'uncommitted writes cannot issue a wake hint');
});

test('notification authentication and revocation use the current mailbox grant',async()=>{
 const subscriptions=[];
 const f=await fixture({subscribe:(device,grant)=>{subscriptions.push({device,token:grant.token});return new Response(null,{status:204})},refresh:async()=>{}});
 const connect=token=>createHandler(f.storage,f.bucket,'test-operator-secret','space-12345678901',()=>Date.now(),{subscribe:(d,g)=>{subscriptions.push({device:d,token:g.token});return new Response(null,{status:204})}})(new Request('https://relay.test/v1/notifications',{headers:{Authorization:'Bearer '+token,Upgrade:'websocket'}}));
 assert.equal((await connect('forged')).status,403);assert.equal(subscriptions.length,0);
 assert.equal((await connect(f.tb)).status,204);assert.equal(subscriptions[0].device,f.b);
 assert.equal((await f.invoke('/v1/notifications','GET',f.tb)).status,426);
 await f.invoke('/v1/enrollment/revoke','POST',f.tb,{});
 assert.equal((await connect(f.tb)).status,403);assert.equal(subscriptions.length,1);
});

test('hibernated socket attachments are rechecked on renewal, revocation, authority change and expiry',async()=>{
 const f=await fixture(),sockets=[];
 const old=await f.storage.get('device:'+f.b);
 for(const a of [{device:f.b,token:old.token},{device:f.a,token:'old-credential'},null])sockets.push({closed:false,deserializeAttachment:()=>a,close(){this.closed=true}});
 const ctx={storage:f.storage,getWebSockets:()=>sockets};
 // Construct a new instance, as the runtime does after hibernation; no in-memory
 // authentication cache is available.
 await new Mailbox(ctx,{ENROLLMENT_ADMIN:'test-operator-secret'}).refresh();
 assert.deepEqual(sockets.map(s=>s.closed),[false,true,true]);
 await f.invoke('/v1/enrollment/revoke','POST',f.tb,{});
 await new Mailbox(ctx,{ENROLLMENT_ADMIN:'test-operator-secret'}).refresh();assert.ok(sockets.every(s=>s.closed));
 const grant=await f.storage.get('device:'+f.a);
 const socket={closed:false,deserializeAttachment:()=>({device:f.a,token:grant.token}),close(){this.closed=true}};
 ctx.getWebSockets=()=>[socket];
 await new Mailbox(ctx,{ENROLLMENT_ADMIN:'rotated-operator-secret'}).refresh();assert.ok(socket.closed);
 socket.closed=false;await f.storage.put('device:'+f.a,{...grant,expires:1});
 await new Mailbox(ctx,{ENROLLMENT_ADMIN:'test-operator-secret'}).refresh();assert.ok(socket.closed);
});
