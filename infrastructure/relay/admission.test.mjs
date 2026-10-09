import test from 'node:test';
import assert from 'node:assert/strict';
import {generateKeyPairSync,sign} from 'node:crypto';
import {decodeJwt} from 'jose';
import worker,{Authorization,createHandler,LIMITS,maintain} from './worker.mjs';
import {digest} from './authorization.mjs';
import {createAdmissionHandler,maintainAdmission,ADMISSION_LIMITS} from './admission.mjs';
class Storage {
 constructor(){this.values=new Map();this.alarm=null;this.tail=Promise.resolve()}
 async get(k){return structuredClone(this.values.get(k))}
 async put(k,v){this.values.set(k,structuredClone(v))}
 async delete(k){this.values.delete(k)}
 async list({prefix='',limit=Infinity}={}){return new Map([...this.values].filter(([k])=>k.startsWith(prefix)).sort(([a],[b])=>a.localeCompare(b)).slice(0,limit))}
 async getAlarm(){return this.alarm}
 async setAlarm(value){this.alarm=value}
 async transaction(fn){let release;const before=this.tail;this.tail=new Promise(r=>release=r);await before;const tx=new Storage();tx.values=structuredClone(this.values);try{const value=await fn(tx);this.values=tx.values;return value}finally{release()}}
}
async function cloudIdentity(incarnation='c'.repeat(32),provider='claude-hosted'){
 const {publicKey,privateKey}=generateKeyPairSync('ed25519'),signing=publicKey.export({type:'spki',format:'der'}).subarray(-32),recipient='age1'+'q'.repeat(58),endpoint='cloud/'+provider+'/'+incarnation;
 const id=await digest(Buffer.concat([Buffer.from('hopsesh-relay-identity-v1\0'),signing,Buffer.from(recipient+'\0'+endpoint)]));
 return {public:{id,endpoint,recipient,signing:signing.toString('base64')},privateKey,incarnation};
}
async function fixture(){
 const storage=new Storage();let now=Math.floor(Date.now()/1000),active=true,enrolled=0,revoked=0;
 const principal='a'.repeat(64),issuer='native-device-12345',credential='b'.repeat(64);
 const enroll=async(space,device,ttl,parent)=>{assert.deepEqual(parent,{device:issuer,credential});enrolled++;return {space,device,token:'scoped-cloud-credential',expires:now+ttl}};
 const authorize=async(space,device,proof)=>active&&space===principal&&device===issuer&&proof===credential;
 const revoke=async(space,device,parent)=>{assert.equal(space,principal);assert.equal(parent.device,issuer);revoked++};
 const handler=createAdmissionHandler(storage,enroll,authorize,revoke,()=>now*1000);
 const call=(path,body,headers={})=>handler(new Request('https://relay.test'+path,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded',...headers},body:new URLSearchParams(body)}));
 const nativeHeaders={'X-Hopsesh-Principal':principal,'X-Hopsesh-Issuer':issuer,'X-Hopsesh-Credential':credential};
 const issue=async(session='session-fixture')=>{const r=await call('/v1/cloud/tickets',{provider:'claude-hosted',session,lease_seconds:'3600'},nativeHeaders);assert.equal(r.status,201);return r.json()};
 const claimArgs=(ticket,identity,extra={})=>{
  const body={ticket:ticket.ticket,identity:JSON.stringify(identity.public),provider:'claude-hosted',session:ticket.session,incarnation:identity.incarnation,lease_expires:String(now+3600),...extra};
  const message='hopsesh-cloud-admission-v1\0https://relay.test\0'+['ticket','identity','provider','session','incarnation','lease_expires'].map(k=>body[k]||'').join('\0');
  return {...body,proof:sign(null,Buffer.from(message),identity.privateKey).toString('base64')};
 };
 return {storage,call,nativeHeaders,issue,claimArgs,counts:()=>({enrolled,revoked}),revokeIssuer:()=>active=false,advance:s=>now+=s,now:()=>now};
}
test('ticket claims one fresh cloud identity only; retries are stable, original and forks stay independent',async()=>{
 const f=await fixture(),ticket=await f.issue(),original=await cloudIdentity(),args=f.claimArgs(ticket,original);
 let status=await(await f.call('/v1/cloud/status',{ticket:ticket.ticket},f.nativeHeaders)).json();assert.equal(status.status,'pending');assert.equal(status.public,null);assert.ok(!('token'in status));
 assert.ok(!JSON.stringify([...f.storage.values]).includes(ticket.ticket),'ticket secret stored in plaintext');
 let r=await f.call('/v1/cloud/claim',args);assert.equal(r.status,200);const connection=await r.json();assert.equal(connection.device,original.public.id);assert.equal(connection.kind,'cloud-session');assert.equal(connection.provisional,true);
 status=await(await f.call('/v1/cloud/status',{ticket:ticket.ticket},f.nativeHeaders)).json();assert.equal(status.status,'claimed');assert.deepEqual(status.public,original.public);assert.equal(status.leaseExpires,connection.expires);assert.ok(!JSON.stringify(status).includes(connection.token));
 assert.deepEqual(await(await f.call('/v1/cloud/claim',args)).json(),connection);assert.equal(f.counts().enrolled,1);
 const rebuilt=await cloudIdentity('d'.repeat(32));assert.equal((await f.call('/v1/cloud/claim',f.claimArgs(ticket,rebuilt))).status,409);
 const forkTicket=await f.issue('fork-fixture'),fork=await cloudIdentity('e'.repeat(32));assert.equal((await f.call('/v1/cloud/claim',f.claimArgs(forkTicket,fork))).status,200);assert.equal(f.counts().enrolled,2);
 assert.deepEqual(await(await f.call('/v1/cloud/claim',args)).json(),connection,'fork changed original admission');
});
test('wrong provider, session, proof, endpoint, expiry and revoked issuer cannot claim a leaf',async()=>{
 const f=await fixture(),ticket=await f.issue(),id=await cloudIdentity();
 for(const extra of [{session:'other-session'},{provider:'codex-current'},{incarnation:'d'.repeat(32)},{lease_expires:String(f.now()+86401)}])assert.notEqual((await f.call('/v1/cloud/claim',f.claimArgs(ticket,id,extra))).status,200);
 const args=f.claimArgs(ticket,id);assert.equal((await f.call('/v1/cloud/claim',{...args,proof:Buffer.alloc(64).toString('base64')})).status,400);
 f.revokeIssuer();assert.equal((await f.call('/v1/cloud/claim',args)).status,403);assert.equal(f.counts().enrolled,0);
});
test('expired admissions are reclaimed but claimed leases retain their revocation record',async()=>{
 const f=await fixture(),ticket=await f.issue(),id=await cloudIdentity();
 await f.call('/v1/cloud/claim',f.claimArgs(ticket,id));const unclaimed=await f.issue('another-session');
 f.advance(601);await maintainAdmission(f.storage,f.now());assert.equal((await f.storage.list({prefix:'ticket:'})).size,1);
 assert.equal((await f.call('/v1/cloud/claim',f.claimArgs(unclaimed,id))).status,400);
 assert.equal((await f.call('/v1/cloud/revoke',{ticket:ticket.ticket},f.nativeHeaders)).status,200);assert.equal(f.counts().revoked,1);
 f.advance(3601);await maintainAdmission(f.storage,f.now());assert.equal(f.storage.values.size,0);
});
test('admission quotas and creator authorization refuse writes before any enrollment',async()=>{
 const f=await fixture();assert.equal((await f.call('/v1/cloud/tickets',{provider:'claude-hosted',session:'one',lease_seconds:'3600'})).status,403);
 for(let n=0;n<ADMISSION_LIMITS.perDevice;n++)await f.issue('session-'+n);
 assert.equal((await f.call('/v1/cloud/tickets',{provider:'claude-hosted',session:'overflow',lease_seconds:'3600'},f.nativeHeaders)).status,429);
 assert.deepEqual(f.counts(),{enrolled:0,revoked:0});
});
test('real Authorization and Mailbox scope cloud routing to its issuer and revoke delivery',async()=>{
 const authStorage=new Storage(),mailboxStorage=new Storage(),space='a'.repeat(64),admin='operator-secret-with-at-least-32-bytes',bucket={put:async()=>{},get:async()=>null,delete:async()=>{}};
 const mailbox=createHandler(mailboxStorage,bucket,admin,space);
 const adminCall=(body,path='/v1/enrollment/register')=>mailbox(new Request('https://relay.test'+path,{method:'POST',headers:{Authorization:'Bearer '+admin},body:JSON.stringify(body)}));
 const native=await(await adminCall({device:'native-device-12345',ttl:3600})).json();
 const second=await(await adminCall({device:'other-device-12345',ttl:3600})).json();
 const obj=new Authorization({storage:authStorage},{ENROLLMENT_ADMIN:admin,MAILBOX:{idFromName(s){assert.equal(s,space);return s},get(){return {fetch:mailbox}}}});
 const headers={'Content-Type':'application/x-www-form-urlencoded','X-Hopsesh-Principal':space,'X-Hopsesh-Issuer':native.device,'X-Hopsesh-Credential':await digest(native.token)};
 const ticketResponse=await obj.fetch(new Request('https://relay.test/v1/cloud/tickets',{method:'POST',headers,body:new URLSearchParams({provider:'claude-hosted',session:'test-session',lease_seconds:'3600'})}));assert.equal(ticketResponse.status,201);
 const ticket=await ticketResponse.json(),f=await fixture(),identity=await cloudIdentity(),args=f.claimArgs(ticket,identity);
 const claim=await obj.fetch(new Request('https://relay.test/v1/cloud/claim',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(args)}));assert.equal(claim.status,200);const connection=await claim.json();assert.equal(decodeJwt(connection.token).kind,'cloud-session');
 const envelope={protocol:1,kind:'response',space,from:identity.public.id,to:second.device,id:'message-123456789',operation:'operation-123456',created:Math.floor(Date.now()/1000),expires:Math.floor(Date.now()/1000)+60,ciphertext:'opaque',signature:'x'.repeat(88)};
 assert.equal((await mailbox(new Request('https://relay.test/v1/messages',{method:'POST',headers:{Authorization:'Bearer '+connection.token},body:JSON.stringify(envelope)}))).status,403);
 const revoke=await obj.fetch(new Request('https://relay.test/v1/cloud/revoke',{method:'POST',headers,body:new URLSearchParams({ticket:ticket.ticket})}));assert.equal(revoke.status,200);
 assert.equal((await mailbox(new Request('https://relay.test/v1/messages',{headers:{Authorization:'Bearer '+connection.token}}))).status,403);
});
test('full mailbox admission is a capacity refusal, leaves the ticket pending and can succeed after expiry',async()=>{
 const authStorage=new Storage(),mailboxStorage=new Storage(),space='a'.repeat(64),admin='operator-secret-with-at-least-32-bytes';
 const now=Date.now(),bucket={put:async()=>{},get:async()=>null,delete:async()=>{}};
 const mailbox=createHandler(mailboxStorage,bucket,admin,space,()=>now);
 const register=device=>mailbox(new Request('https://relay.test/v1/enrollment/register',{method:'POST',headers:{Authorization:'Bearer '+admin},body:JSON.stringify({device,ttl:3600})}));
 const native=await(await register('native-device-12345')).json();
 for(let i=1;i<LIMITS.devices;i++)assert.equal((await register('filler-device-'+String(i).padStart(4,'0'))).status,201);
 const obj=new Authorization({storage:authStorage},{ENROLLMENT_ADMIN:admin,MAILBOX:{idFromName:s=>s,get:()=>({fetch:mailbox})}});
 const headers={'Content-Type':'application/x-www-form-urlencoded','X-Hopsesh-Principal':space,'X-Hopsesh-Issuer':native.device,'X-Hopsesh-Credential':await digest(native.token)};
 const call=(path,body,native=false)=>obj.fetch(new Request('https://relay.test'+path,{method:'POST',headers:native?headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(body)}));
 const ticket=await(await call('/v1/cloud/tickets',{provider:'claude-hosted',session:'capacity-fixture',lease_seconds:'3600'},true)).json();
 const f=await fixture(),identity=await cloudIdentity(),args=f.claimArgs(ticket,identity);
 const refused=await call('/v1/cloud/claim',args);
 assert.equal(refused.status,409);assert.deepEqual(await refused.json(),{error:'capacity_exceeded'});
 assert.equal(await mailboxStorage.get('device-count'),LIMITS.devices);
 assert.equal(await mailboxStorage.get('device:'+identity.public.id),undefined);
 assert.equal((await(await call('/v1/cloud/status',{ticket:ticket.ticket},true)).json()).status,'pending');
 // Expire only a filler through the real cleanup path; the native issuer stays valid.
 const filler=await mailboxStorage.get('device:filler-device-0001');filler.expires=Math.floor(now/1000)-1;await mailboxStorage.put('device:filler-device-0001',filler);
 await maintain(mailboxStorage,bucket,Math.floor(now/1000));
 const admitted=await call('/v1/cloud/claim',args);assert.equal(admitted.status,200);
 assert.equal((await admitted.json()).device,identity.public.id);
 assert.equal(await mailboxStorage.get('device-count'),LIMITS.devices);
});
test('a cloud credential cannot issue another ticket or forge issuer headers before paid allocation',async()=>{
 let allocated=0;const space='a'.repeat(64),admin='operator-secret-with-at-least-32-bytes',storage=new Storage(),mailbox=createHandler(storage,{put:async()=>{}},admin,space);
 const reg=async body=>(await mailbox(new Request('https://relay.test/v1/enrollment/register',{method:'POST',headers:{Authorization:'Bearer '+admin},body:JSON.stringify(body)}))).json();
 const native=await reg({device:'native-device-12345',ttl:3600}),cloud=await reg({device:'cloud-device-123456',kind:'cloud-session',issuer:{device:native.device,credential:await digest(native.token)},ttl:3600});
 const env={ENROLLMENT_ADMIN:admin,LOGIN_RATE:{limit:async()=>({success:true})},CODE_RATE:{limit:async()=>({success:true})},AUTHORIZATION:{idFromName(){allocated++;return 'object'},get(){assert.fail()}}};
 const r=await worker.fetch(new Request('https://relay.test/v1/cloud/tickets',{method:'POST',headers:{Authorization:'Bearer '+cloud.token,'CF-Connecting-IP':'192.0.2.1','X-Hopsesh-Principal':space,'X-Hopsesh-Issuer':native.device},body:''}),env);
 assert.equal(r.status,403);assert.equal(allocated,0);
});
test('a lost cross-object commit retries the same credential and cannot resurrect revoked admission',async()=>{
 class FailingCommit extends Storage {async transaction(fn){const before=structuredClone(this.values),result=await super.transaction(fn);if(this.fail){this.fail=false;this.values=before;throw new Error('injected commit failure')}return result}}
 const authStorage=new FailingCommit(),mailboxStorage=new Storage(),space='a'.repeat(64),admin='operator-secret-with-at-least-32-bytes',bucket={put:async()=>{},get:async()=>null,delete:async()=>{}};
 const mailbox=createHandler(mailboxStorage,bucket,admin,space),adminCall=(body,path='/v1/enrollment/register')=>mailbox(new Request('https://relay.test'+path,{method:'POST',headers:{Authorization:'Bearer '+admin},body:JSON.stringify(body)}));
 const native=await(await adminCall({device:'native-device-12345',ttl:3600})).json(),obj=new Authorization({storage:authStorage},{ENROLLMENT_ADMIN:admin,MAILBOX:{idFromName:s=>s,get:()=>({fetch:mailbox})}});
 const headers={'Content-Type':'application/x-www-form-urlencoded','X-Hopsesh-Principal':space,'X-Hopsesh-Issuer':native.device,'X-Hopsesh-Credential':await digest(native.token)};
 const call=(path,body,native=false)=>obj.fetch(new Request('https://relay.test'+path,{method:'POST',headers:native?headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(body)}));
 const ticket=await(await call('/v1/cloud/tickets',{provider:'claude-hosted',session:'commit-failure',lease_seconds:'3600'},true)).json();
 const f=await fixture(),identity=await cloudIdentity(),args=f.claimArgs(ticket,identity);
 authStorage.fail=true;assert.equal((await call('/v1/cloud/claim',args)).status,503);
 const firstGrant=await mailboxStorage.get('device:'+identity.public.id);assert.ok(firstGrant,'mailbox did not commit the first credential');
 const retried=await call('/v1/cloud/claim',args);assert.equal(retried.status,200);const connection=await retried.json();assert.equal(await digest(connection.token),firstGrant.token);assert.deepEqual(await mailboxStorage.get('device:'+identity.public.id),firstGrant);
 assert.equal((await call('/v1/cloud/revoke',{ticket:ticket.ticket},true)).status,200);
 assert.equal((await call('/v1/cloud/claim',args)).status,400);
 assert.equal((await call('/v1/cloud/revoke',{ticket:ticket.ticket},true)).status,200);
 // Expiry reclamation is idempotent for a ticket's known target.
 await mailboxStorage.delete('device:'+identity.public.id);
 assert.equal((await call('/v1/cloud/revoke',{ticket:ticket.ticket},true)).status,200);
});
test('native renewal and operator key rotation invalidate unclaimed admissions',async()=>{
 for(const rotate of [false,true]){
  const authStorage=new Storage(),mailboxStorage=new Storage(),space='a'.repeat(64),admin='operator-secret-with-at-least-32-bytes',bucket={put:async()=>{},get:async()=>null,delete:async()=>{}};
  let currentAdmin=admin;
  const mailbox=req=>createHandler(mailboxStorage,bucket,currentAdmin,space)(req);
  const register=()=>mailbox(new Request('https://relay.test/v1/enrollment/register',{method:'POST',headers:{Authorization:'Bearer '+currentAdmin},body:JSON.stringify({device:'native-device-12345',ttl:3600})}));
  const native=await(await register()).json(),env={ENROLLMENT_ADMIN:admin,MAILBOX:{idFromName:s=>s,get:()=>({fetch:mailbox})}};
  let obj=new Authorization({storage:authStorage},env);
  const headers={'Content-Type':'application/x-www-form-urlencoded','X-Hopsesh-Principal':space,'X-Hopsesh-Issuer':native.device,'X-Hopsesh-Credential':await digest(native.token)};
  const ticket=await(await obj.fetch(new Request('https://relay.test/v1/cloud/tickets',{method:'POST',headers,body:new URLSearchParams({provider:'claude-hosted',session:'renewal-fixture',lease_seconds:'3600'})}))).json();
  if(rotate){currentAdmin='rotated-operator-secret-at-least-32-bytes';env.ENROLLMENT_ADMIN=currentAdmin;obj=new Authorization({storage:authStorage},env)}else assert.equal((await register()).status,201);
  const f=await fixture(),identity=await cloudIdentity(),args=f.claimArgs(ticket,identity);
  const result=await obj.fetch(new Request('https://relay.test/v1/cloud/claim',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(args)}));
  assert.equal(result.status,403);assert.equal(await mailboxStorage.get('device:'+identity.public.id),undefined);
 }
});
test('outer Worker supplies proof origin and strips caller-supplied admission authority',async()=>{
 const env={ENROLLMENT_ADMIN:'operator-secret-with-at-least-32-bytes',LOGIN_RATE:{limit:async()=>({success:true})},CODE_RATE:{limit:async()=>({success:true})},AUTHORIZATION:{idFromName:name=>name,get:()=>({fetch:async req=>{
  assert.equal(req.headers.get('X-Hopsesh-External-Origin'),'https://relay.test');
  for(const name of ['X-Hopsesh-Principal','X-Hopsesh-Issuer','X-Hopsesh-Credential'])assert.equal(req.headers.get(name),null);
  return new Response('{}');
 }})}};
 const result=await worker.fetch(new Request('https://relay.test/v1/cloud/claim',{method:'POST',headers:{'CF-Connecting-IP':'192.0.2.1','X-Hopsesh-External-Origin':'https://attacker.test','X-Hopsesh-Principal':'a'.repeat(64),'X-Hopsesh-Issuer':'spoofed-native-device','X-Hopsesh-Credential':'b'.repeat(64)},body:'fixture'}),env);
 assert.equal(result.status,200);
});
