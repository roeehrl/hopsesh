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
async function fixture(){
 const storage=new Storage(),bucket=new Bucket(),space='space-12345678901',admin='test-operator-secret';let clock=Date.now();
 const handle=createHandler(storage,bucket,admin,space,()=>clock);
 const invoke=(path,method='GET',token='',body)=>handle(new Request('https://relay.test'+path,{method,headers:{Authorization:'Bearer '+token,'X-Hopsesh-Space':space},body:body===undefined?undefined:JSON.stringify(body)}));
 const enroll=async device=>{const r=await invoke('/v1/enrollment/register','POST',admin,{device,ttl:3600});assert.equal(r.status,201);return(await r.json()).token};
 const a='endpoint-A-123456',b='endpoint-B-123456',ta=await enroll(a),tb=await enroll(b);
 const envelope=(id='message-123456789')=>({kind:"request",protocol:1,id,space,from:a,to:b,operation:'operation-123456',created:Math.floor(clock/1000),expires:Math.floor(clock/1000)+300,ciphertext:'opaque-encrypted-payload',signature:'x'.repeat(88)});
 return{storage,bucket,invoke,ta,tb,a,b,envelope,advance:ms=>clock+=ms};
}
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
