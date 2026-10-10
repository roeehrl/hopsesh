import {SignJWT, jwtVerify} from 'jose';
import {createAuthorizationHandler, maintainAuthorization, accessPrincipal, response} from './authorization.mjs';
import {deviceAsset} from './device-page.mjs';
import {createAdmissionHandler,maintainAdmission,AdmissionCapacityError} from './admission.mjs';
import {digest} from './authorization.mjs';
// Experimental Hopsesh-only infrastructure. Payloads stay encrypted end to end.
const encoder = new TextEncoder();
export const LIMITS = Object.freeze({ frame: 24 * 1024 * 1024, bytes: 64 * 1024 * 1024, messages: 1000, devices: 32, lifetime: 86400, batch: 1, dailyFrames:4096, dailyBytes:256*1024*1024 });
const opaque = s => typeof s === 'string' && /^[A-Za-z0-9_-]{16,128}$/.test(s);
const hash = async s => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(s)))).map(v => v.toString(16).padStart(2,'0')).join('');
const json = (v,status=200) => new Response(JSON.stringify(v),{status,headers:{'Content-Type':'application/json','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'}});
async function schedule(storage,when,now=Date.now()){
 const old=await storage.getAlarm();if(!old||old<=now||when<old)await storage.setAlarm(when);
}
// Persist with the message, before attempting its best-effort WebSocket hint.
// One coalesced retry per recipient survives hibernation; ACK ends the demand.
async function armNotification(storage,device,now){
 const quota=await storage.get('quota:'+device);if(!quota?.count)return;
 const previous=await storage.get('notify:'+device);
 const pending={sequence:await storage.get('sequence:'+device),at:Math.min(previous?.at??Infinity,now+1000),delay:1000};
 await storage.put('notify:'+device,pending);await schedule(storage,pending.at,now);
}
async function bounded(req, max) {
  const advertised=Number(req.headers.get('content-length')||0);
  if(advertised>max)throw new Error('too-large');
  const reader=req.body?.getReader();if(!reader)throw new Error('bad-body');
  let length=0;const chunks=[];
  for(;;){const {value,done}=await reader.read();if(done)break;length+=value.length;if(length>max){await reader.cancel();throw new Error('too-large')}chunks.push(value)}
  const result=new Uint8Array(length);let offset=0;for(const c of chunks){result.set(c,offset);offset+=c.length}
  return JSON.parse(new TextDecoder().decode(result));
}
// This exact handler is used by the deployed Durable Object and deterministic
// tests. Storage transactions serialize quotas, revocation and cursor updates.
export function createHandler(storage,bucket,adminToken,space,clock=()=>Date.now(),events,policy={}) {
 return async req => {
  let recipient;
  const handle=async()=>{
  const url=new URL(req.url),path=url.pathname,now=Math.floor(clock()/1000);
  try {
   if(path==='/v1/capabilities'&&req.method==='GET')return json({protocol:1,limits:LIMITS,methods:['GET','POST'],experimental:true});
   if(!opaque(space))return json({error:'space'},400);
   const token=(req.headers.get('authorization')||'').replace(/^Bearer /,'');
   if(path==='/v1/operator/stats'){
    if(req.method!=='GET'||url.search)return json({error:'method'},405);
    if(!adminToken||!token||await hash(token)!==await hash(adminToken))return json({error:'authorization'},403);
    const counters={devices:0,messages:0,ciphertextBytes:0,pendingDeletions:0,tombstones:0};
    for(const [,d]of await storage.list({prefix:'device:'}))if(!d.revoked&&d.expires>now)counters.devices++;
    for(const [,m]of await storage.list({prefix:'message:'})){counters.messages++;counters.ciphertextBytes+=m.bytes}
    counters.pendingDeletions=(await storage.list({prefix:'delete:'})).size;counters.tombstones=(await storage.list({prefix:'id:'})).size;
    const day=Math.floor(now/86400),stored=await storage.get('day-budget'),budget=stored?.day===day?stored:{day,frames:0,bytes:0};
    return json({...counters,day:budget.day,admittedFrames:budget.frames,admittedBytes:budget.bytes,resetAt:(day+1)*86400,paused:policy.paused===true,limits:{dailyFrames:ceiling(policy.dailyFrames,LIMITS.dailyFrames),dailyBytes:ceiling(policy.dailyBytes,LIMITS.dailyBytes)}});
   }
   if(policy.paused===true&&((path==='/v1/messages'&&req.method==='POST')||path==='/v1/enrollment/register'))return json({error:'paused'},503);
   if(['/v1/enrollment/register','/v1/enrollment/check','/v1/enrollment/revoke-device'].includes(path)){
    if(req.method!=='POST')return json({error:'method'},405);
    // Operator authorization only enrolls a routing credential. Local fingerprint
    // approval independently grants peer actions; this endpoint cannot widen it.
    if(!adminToken||!token||await hash(token)!==await hash(adminToken))return json({error:'authorization'},403);
    const body=await bounded(req,4096);
    if(path==='/v1/enrollment/check'){
     const grant=opaque(body.device)&&await storage.get('device:'+body.device);
     return json({active:!!grant&&!grant.revoked&&grant.expires>now&&grant.token===body.credential&&grant.kind==='device'&&grant.authority===await hash(adminToken),expires:grant?.expires||0});
    }
    if(path==='/v1/enrollment/revoke-device')return await storage.transaction(async tx=>{
     const issuer=body.issuer&&await tx.get('device:'+body.issuer.device),target=opaque(body.device)&&await tx.get('device:'+body.device);
     if(!issuer||issuer.revoked||issuer.expires<=now||issuer.kind!=='device'||issuer.token!==body.issuer.credential||issuer.authority!==await hash(adminToken)||!opaque(body.device)||target&&(target.kind!=='cloud-session'||target.issuer!==body.issuer.device))return json({error:'authorization'},403);
     // An expired target may already have been reclaimed. The Authorization
     // object's owned ticket is still revoked; repeating that action is safe.
     if(!target)return json({revoked:true});
     await tx.put('device:'+body.device,{...target,revoked:true});await tx.delete('token:'+target.token);return json({revoked:true});
    });
    const kind=body.kind||'device';if(!['device','cloud-session'].includes(kind))return json({error:'enrollment'},400);
    if(!opaque(body.device)||!Number.isInteger(body.ttl)||body.ttl<(kind==='cloud-session'?1:60)||body.ttl>LIMITS.lifetime)return json({error:'enrollment'},400);
    if(body.admission!==undefined&&(kind!=='cloud-session'||! /^[a-f0-9]{64}$/.test(body.admission)))return json({error:'enrollment'},400);
    if(kind==='cloud-session'&&!body.issuer)return json({error:'enrollment'},400);
    // Preserve the absolute signed lease across Authorization -> Mailbox delay.
    // A relative TTL recomputed by each object would extend the cloud's authority.
    if(body.expires!==undefined&&(!Number.isInteger(body.expires)||body.expires<=now||kind!=='cloud-session'))return json({error:'enrollment'},400);
    if(body.admission&&body.expires===undefined)return json({error:'enrollment'},400);
    let ttl=body.expires===undefined?body.ttl:Math.min(body.ttl,body.expires-now);
    if(body.issuer){const issuer=await storage.get('device:'+body.issuer.device);if(kind!=='cloud-session'||!issuer||issuer.revoked||issuer.kind!=='device'||issuer.expires<=now||issuer.token!==body.issuer.credential||issuer.authority!==await hash(adminToken))return json({error:'authorization'},403);ttl=Math.min(ttl,issuer.expires-now);if(ttl<1)return json({error:'authorization'},403)}
    const secret=Array.from(crypto.getRandomValues(new Uint8Array(32))).map(v=>v.toString(16).padStart(2,'0')).join('');
    const credential=await new SignJWT({space,device:body.device,kind,nonce:secret}).setProtectedHeader({alg:'HS256',typ:'JWT'}).setIssuer('hopsesh-relay-v1').setAudience('hopsesh-relay-mailbox').setIssuedAt(now).setExpirationTime(now+ttl).sign(encoder.encode(adminToken));
    const key=await hash(credential);
    return await storage.transaction(async tx=>{
     const count=(await tx.get('device-count'))||0;
     const old=await tx.get('device:'+body.device);
     if(!old&&count>=LIMITS.devices)return json({error:'quota'},429);
     if(body.issuer){const issuer=await tx.get('device:'+body.issuer.device);if(!issuer||issuer.revoked||issuer.kind!=='device'||issuer.expires<now+ttl||issuer.token!==body.issuer.credential||issuer.authority!==await hash(adminToken))return json({error:'authorization'},403)}
     // Reconstruct the same credential after a cross-object response or commit
     // failure. Never rotate or resurrect this admission on retry.
     if(body.admission&&old){
      if(old.admission!==body.admission||old.revoked||old.expires<=now||old.kind!==kind||old.issuer!==body.issuer.device||old.authority!==await hash(adminToken)||old.expires>body.expires)return json({error:'admission-conflict'},409);
      const replay=await new SignJWT({space,device:body.device,kind,nonce:old.nonce}).setProtectedHeader({alg:'HS256',typ:'JWT'}).setIssuer('hopsesh-relay-v1').setAudience('hopsesh-relay-mailbox').setIssuedAt(old.issuedAt).setExpirationTime(old.expires).sign(encoder.encode(adminToken));
      if(await hash(replay)!==old.token)return json({error:'admission-conflict'},409);
      return json({token:replay,space,device:body.device,expires:old.expires},201);
     }
     if(old)await tx.delete('token:'+old.token);
     await tx.put('device:'+body.device,{token:key,expires:now+ttl,revoked:false,kind,issuer:body.issuer?.device,authority:await hash(adminToken),...(body.admission?{admission:body.admission,nonce:secret,issuedAt:now}:{})});
     await tx.put('token:'+key,body.device);
     if(!old)await tx.put('device-count',count+1);
     await schedule(tx,(now+ttl)*1000,clock());
     return json({token:credential,space,device:body.device,expires:now+ttl},201);
    });
   }
   const tokenHash=await hash(token);
   const device=await storage.get('token:'+tokenHash);
   const grant=device&&await storage.get('device:'+device);
   if(!grant||grant.revoked||grant.expires<=now||grant.token!==tokenHash||grant.authority!==await hash(adminToken))return json({error:'authorization'},403);
   if(path==='/v1/notifications'){
    if(req.method!=='GET'||req.headers.get('Upgrade')?.toLowerCase()!=='websocket'||url.search)return json({error:'websocket-required'},426);
    return events?.subscribe?await events.subscribe(device,grant):json({error:'notifications-unavailable'},501);
   }
   if(path==='/v1/enrollment/revoke'){
    if(req.method!=='POST')return json({error:'method'},405);
    // A scoped credential may revoke itself, never another endpoint.
    return await storage.transaction(async tx=>{await tx.put('device:'+device,{...grant,revoked:true});await tx.delete('token:'+tokenHash);return json({revoked:true})});
   }
   if(path==='/v1/messages'&&req.method==='POST'){
    const e=await bounded(req,LIMITS.frame);
    if(grant.kind==='cloud-session'&&(e.to!==grant.issuer||e.kind!=='response'))return json({error:'recipient'},403);
    if(!['request','response','observation'].includes(e.kind)||e.protocol!==1||e.space!==space||e.from!==device||!opaque(e.to)||e.to===device||!opaque(e.id)||!opaque(e.operation)||!Number.isInteger(e.created)||!Number.isInteger(e.expires)||e.expires<=now||e.created>now+60||e.expires<=e.created||e.expires-e.created>LIMITS.lifetime||typeof e.ciphertext!=='string'||typeof e.signature!=='string'||!e.ciphertext.length||e.signature.length!==88)return json({error:'envelope'},400);
    const target=await storage.get('device:'+e.to);if(!target||target.revoked||target.expires<=now)return json({error:'recipient'},403);
    const data=JSON.stringify(e),wireBytes=encoder.encode(data).length,sum=await hash(data),key=space+'/'+e.to+'/'+e.id;
    recipient=e.to;
    return await storage.transaction(async tx=>{
     const fresh=await tx.get('device:'+device),receiver=await tx.get('device:'+e.to);
     if(!fresh||fresh.revoked||fresh.expires<=now||fresh.token!==tokenHash||!receiver||receiver.revoked||receiver.expires<=now)return json({error:'authorization'},403);
     const prior=await tx.get('id:'+device+':'+e.id);
     if(prior)return prior.sum===sum?json({sequence:prior.sequence,duplicate:true}):json({error:'id-conflict'},409);
     const q=(await tx.get('quota:'+e.to))||{bytes:0,count:0};
     const tombstones=(await tx.get('id-count'))||0;
     if(q.bytes+wireBytes>LIMITS.bytes||q.count>=LIMITS.messages||tombstones>=10000)return json({error:'quota'},429);
     const day=Math.floor(now/86400),stored=await tx.get('day-budget'),budget=stored?.day===day?stored:{day,frames:0,bytes:0};
     if(budget.frames>=ceiling(policy.dailyFrames,LIMITS.dailyFrames)||budget.bytes+wireBytes>ceiling(policy.dailyBytes,LIMITS.dailyBytes))return json({error:'traffic-budget'},429);
     const sequence=((await tx.get('sequence:'+e.to))||0)+1;
     // If bucket publication or the transaction fails, no delivery is visible.
     // Unreferenced ciphertext has a separate R2 lifecycle cleanup policy.
     await bucket.put(key,data,{customMetadata:{expires:String(e.expires)}});
     await tx.put('message:'+e.to+':'+String(sequence).padStart(20,'0'),{key,bytes:wireBytes,expires:e.expires,sequence});
     await tx.put('id:'+device+':'+e.id,{sum,sequence,expires:e.expires});await tx.put('id-count',tombstones+1);
     await tx.put('sequence:'+e.to,sequence);await tx.put('quota:'+e.to,{bytes:q.bytes+wireBytes,count:q.count+1});
     await tx.put('day-budget',{day,frames:budget.frames+1,bytes:budget.bytes+wireBytes});
     if(events?.changed)await armNotification(tx,e.to,clock());
     await schedule(tx,e.expires*1000,clock());
     return json({sequence},201);
    });
   }
   if(path==='/v1/messages'&&req.method==='GET'){
    const cursor=Number(url.searchParams.get('cursor')||0);
    if(!Number.isSafeInteger(cursor)||cursor<0)return json({error:'cursor'},400);
    const rows=await storage.list({prefix:'message:'+device+':'}),messages=[];let next=cursor;
    for(const [,m]of rows){if(m.sequence<=cursor)continue;next=m.sequence;if(m.expires<=now)continue;const object=await bucket.get(m.key);if(!object)return json({error:'storage'},503);messages.push({sequence:m.sequence,envelope:JSON.parse(await object.text())});if(messages.length>=LIMITS.batch)break}
    return json({messages,cursor:next});
   }
   if(path==='/v1/ack'&&req.method==='POST'){
    const body=await bounded(req,4096),cursor=body.cursor;
    if(!Number.isSafeInteger(cursor)||cursor<0||cursor>((await storage.get('sequence:'+device))||0))return json({error:'cursor'},400);
    await removeMessages(storage,bucket,device,m=>m.sequence<=cursor);
    return json({acknowledged:cursor});
   }
   return json({error:'method-or-route'},['/v1/messages','/v1/ack'].includes(path)?405:404);
  }catch(e){return json({error:e?.message==='too-large'?'frame':e?.message==='storage'?'storage':'invalid-request'},e?.message==='too-large'?413:e?.message==='storage'?503:400)}
  };
  const result=await handle();
  // Hints follow the committed transaction. A failed hint must never turn an
  // accepted encrypted message into an apparent publication failure.
  if(events&&result.ok){try{
   if(recipient&&result.status===201)await events.changed(recipient);
   if(new URL(req.url).pathname.startsWith('/v1/enrollment/'))await events.refresh();
  }catch{/* Durable notification demand and HTTP reconciliation recover missed hints. */}}
  return result;
 };
}
function ceiling(value,max){return Number.isSafeInteger(Number(value))&&Number(value)>=1?Math.min(Number(value),max):max}
function operatorPolicy(env){return {paused:env.RELAY_PAUSED==='1',dailyFrames:env.DAILY_FRAMES_PER_SPACE,dailyBytes:env.DAILY_BYTES_PER_SPACE}}
async function removeMessages(storage,bucket,device,predicate){
 await storage.transaction(async tx=>{
  const rows=await tx.list({prefix:'message:'+device+':' });const q=(await tx.get('quota:'+device))||{bytes:0,count:0};
  // Commit logical deletion and a cleanup intent together. R2 deletion cannot
  // participate in a DO transaction: deleting it first can strand a visible row
  // after rollback and permanently block a mailbox on a missing object.
  for(const [k,m]of rows){if(!predicate(m))continue;await tx.put('delete:'+m.key,m.key);await tx.delete(k);q.bytes-=m.bytes;q.count--;}
  await tx.put('quota:'+device,q);
  if(q.count===0)await tx.delete('notify:'+device);
 });
 await schedule(storage,Date.now()+60000);
 await retryDeletes(storage,bucket);
}
async function retryDeletes(storage,bucket){
 const rows=await storage.list({prefix:'delete:'});
 for(const [key,object]of rows){try{await bucket.delete(object);await storage.delete(key)}catch{throw new Error('storage')}}
}
export async function maintain(storage,bucket,now=Math.floor(Date.now()/1000),reconcileNotifications){
 const devices=await storage.list({prefix:'device:'});
 for(const [key,d]of devices)await removeMessages(storage,bucket,key.slice(7),m=>m.expires<=now||d.expires<=now||d.revoked);
 await storage.transaction(async tx=>{
  let ids=(await tx.get('id-count'))||0,deviceCount=(await tx.get('device-count'))||0;
  for(const[key,r]of await tx.list({prefix:'id:'}))if(r.expires<=now){await tx.delete(key);ids--}
  // Expired routing credentials cannot resurrect an old mailbox. Re-enrollment
  // still needs operator authorization and independent local key approval.
  for(const[key,d]of await tx.list({prefix:'device:'}))if(d.expires<=now||d.revoked){await tx.delete('token:'+d.token);await tx.delete(key);deviceCount--}
  for(const[key]of await tx.list({prefix:'notify:'})){
   const device=key.slice(7),grant=await tx.get('device:'+device),quota=await tx.get('quota:'+device);
   if(!grant||grant.revoked||grant.expires<=now||!quota?.count)await tx.delete(key);
  }
  await tx.put('id-count',Math.max(0,ids));await tx.put('device-count',Math.max(0,deviceCount));
 });
 await retryDeletes(storage,bucket);
 // Resolve pending notification work before computing the next shared alarm,
 // so an old due hint cannot leave an unnecessary one-second wake scheduled.
 if(reconcileNotifications)await reconcileNotifications();
 // Wake at the next real expiry, not every minute while devices are idle.
 let next=Infinity;
 for(const prefix of ['device:','id:','message:'])for(const [,v]of await storage.list({prefix}))next=Math.min(next,v.expires);
 const deletes=await storage.list({prefix:'delete:',limit:1});
 if(deletes.size)next=Math.min(next,now+60);
 for(const[,pending]of await storage.list({prefix:'notify:'}))next=Math.min(next,pending.at/1000);
 if(Number.isFinite(next))await storage.setAlarm(Math.max(Date.now()+1000,next*1000));
}
export class Mailbox {
 constructor(ctx,env){this.ctx=ctx;this.env=env}
 async subscribe(device,grant){
  if(this.ctx.getWebSockets('device:'+device).length>=4||this.ctx.getWebSockets().length>=128)return json({error:'quota'},429);
  const active=await this.ctx.storage.transaction(async tx=>{
   const current=await tx.get('device:'+device);
   if(!current||current.revoked||current.expires<=Date.now()/1000||current.token!==grant.token||current.authority!==grant.authority)return false;
   await armNotification(tx,device,Date.now());return true;
  });
  if(!active)return json({error:'authorization'},403);
  // The durable rearm yields: concurrent handshakes must recheck capacity.
  if(this.ctx.getWebSockets('device:'+device).length>=4||this.ctx.getWebSockets().length>=128)return json({error:'quota'},429);
  const pair=new WebSocketPair(),client=pair[0],server=pair[1];
  this.ctx.acceptWebSocket(server,['device:'+device]);
  server.serializeAttachment({device,token:grant.token});
  server.send('{"type":"mailbox-changed"}');
  return new Response(null,{status:101,webSocket:client});
 }
 async refresh(){
  if(!this.ctx.getWebSockets)return;
  const authority=await hash(this.env.ENROLLMENT_ADMIN),now=Math.floor(Date.now()/1000);
  for(const socket of this.ctx.getWebSockets()){
   const a=socket.deserializeAttachment(),grant=a&&await this.ctx.storage.get('device:'+a.device);
   if(!grant||grant.revoked||grant.expires<=now||grant.token!==a.token||grant.authority!==authority){try{socket.close(1008,'authorization ended')}catch{}}
  }
 }
 async changed(device){
  await this.refresh();
  let online=false;
  for(const socket of this.ctx.getWebSockets('device:'+device)){
   if(socket.readyState!==1)continue;
   online=true;
   try{socket.send('{"type":"mailbox-changed"}')}catch{try{socket.close(1011,'notification delivery failed')}catch{}}
  }
  return online;
 }
 async retryNotifications(){
  const storage=this.ctx.storage,now=Date.now();
  for(const[key,pending]of await storage.list({prefix:'notify:'})){
   if(pending.at>now)continue;
   let online=true;
   try{online=await this.changed(key.slice(7))}catch{/* Retain bounded retry after transient storage failure. */}
   await storage.transaction(async tx=>{
    const current=await tx.get(key);
    // A concurrent publication/ACK owns its newer schedule. Never erase it.
    if(!current||current.sequence!==pending.sequence||current.at!==pending.at)return;
    if(!online){await tx.delete(key);return} // Reconnection rearms any pending mailbox.
    const delay=Math.min(pending.delay*2,30000),at=Date.now()+delay;
    await tx.put(key,{...current,at,delay});await schedule(tx,at);
   });
  }
 }
 async fetch(req){const space=req.headers.get('X-Hopsesh-Space');return createHandler(this.ctx.storage,this.env.CIPHERTEXT,this.env.ENROLLMENT_ADMIN,space,()=>Date.now(),{subscribe:(d,g)=>this.subscribe(d,g),changed:d=>this.changed(d),refresh:()=>this.refresh()},operatorPolicy(this.env))(req)}
 webSocketMessage(socket){socket.close(1008,'notifications are read only')}
 webSocketError(socket){try{socket.close(1011,'notification connection ended')}catch{}}
 async alarm(){
  await maintain(this.ctx.storage,this.env.CIPHERTEXT,Math.floor(Date.now()/1000),async()=>{
   await this.refresh();await this.retryNotifications();
  });
 }
}
export class Authorization {
 constructor(ctx,env){this.ctx=ctx;this.env=env}
 async fetch(req){
  const mailbox=async(space,path,body)=>this.env.MAILBOX.get(this.env.MAILBOX.idFromName(space)).fetch(new Request(new URL(path,req.url),{method:'POST',headers:{'Content-Type':'application/json','X-Hopsesh-Space':space,Authorization:'Bearer '+this.env.ENROLLMENT_ADMIN},body:JSON.stringify(body)}));
  if(new URL(req.url).pathname.startsWith('/v1/cloud/')){
   const authorize=async(space,device,credential)=>{const r=await mailbox(space,'/v1/enrollment/check',{device,credential});return r.ok&&(await r.json()).active===true};
   const enroll=async(space,device,ttl,issuer,admission,expires)=>{const r=await mailbox(space,'/v1/enrollment/register',{device,ttl,kind:'cloud-session',issuer,admission,expires});if(r.status===429)throw new AdmissionCapacityError();if(r.status!==201)throw new Error('enrollment');return r.json()};
   const revoke=async(space,device,issuer)=>{const r=await mailbox(space,'/v1/enrollment/revoke-device',{device,issuer});if(!r.ok)throw new Error('revocation')};
   return createAdmissionHandler(this.ctx.storage,enroll,authorize,revoke)(req);
  }
  const enroll=async(space,device,ttl)=>{
   const r=await this.env.MAILBOX.get(this.env.MAILBOX.idFromName(space)).fetch(new Request(new URL('/v1/enrollment/register',req.url),{method:'POST',headers:{'Content-Type':'application/json','X-Hopsesh-Space':space,Authorization:'Bearer '+this.env.ENROLLMENT_ADMIN},body:JSON.stringify({device,ttl})}));
   if(r.status!==201)throw new Error('enrollment');return r.json();
  };
  return createAuthorizationHandler(this.ctx.storage,enroll)(req);
 }
 async alarm(){await maintainAuthorization(this.ctx.storage);await maintainAdmission(this.ctx.storage)}
}
export default {async fetch(req,env){
 const url=new URL(req.url);
 if(env.RELAY_PAUSED==='1'&&req.method==='POST'&&['/v1/messages','/v1/cloud/tickets','/v1/cloud/claim','/v1/device/code','/v1/authorization/request','/v1/enrollment/register'].includes(url.pathname))return json({error:'paused'},503);
 if(url.pathname==='/v1/capabilities'&&req.method==='GET')return json({protocol:1,limits:LIMITS,experimental:true});
 if(url.pathname.startsWith('/v1/cloud/')){
  if(!env.AUTHORIZATION||!env.LOGIN_RATE||!env.CODE_RATE||!env.ENROLLMENT_ADMIN||env.ENROLLMENT_ADMIN.length<32)return response({error:'temporarily_unavailable'},503);
  if(url.protocol!=='https:'||req.method!=='POST'||!['/v1/cloud/tickets','/v1/cloud/claim','/v1/cloud/revoke','/v1/cloud/status'].includes(url.pathname))return response({error:'invalid_request'},400);
  const headers=new Headers(req.headers);for(const k of ['X-Hopsesh-Principal','X-Hopsesh-Issuer','X-Hopsesh-Credential'])headers.delete(k);
  headers.set('X-Hopsesh-External-Origin',url.origin);
  if(url.pathname!=='/v1/cloud/claim'){
   const token=(req.headers.get('authorization')||'').replace(/^Bearer /,'');if(token.length>4096)return response({error:'access_denied'},403);
   try{const {payload}=await jwtVerify(token,encoder.encode(env.ENROLLMENT_ADMIN),{algorithms:['HS256'],issuer:'hopsesh-relay-v1',audience:'hopsesh-relay-mailbox'});if(!opaque(payload.space)||!opaque(payload.device)||payload.kind!=='device')return response({error:'access_denied'},403);headers.set('X-Hopsesh-Principal',payload.space);headers.set('X-Hopsesh-Issuer',payload.device);headers.set('X-Hopsesh-Credential',await digest(token))}catch{return response({error:'access_denied'},403)}
  }
  const ip=req.headers.get('CF-Connecting-IP');if(!ip)return response({error:'invalid_request'},400);
  if(!(await env.LOGIN_RATE.limit({key:'cloud:'+ip})).success||!(await env.LOGIN_RATE.limit({key:'global:'+url.pathname})).success||url.pathname==='/v1/cloud/claim'&&!(await env.CODE_RATE.limit({key:'cloud:'+ip})).success)return response({error:'quota'},429);
  return env.AUTHORIZATION.get(env.AUTHORIZATION.idFromName('hopsesh-device-enrollment-v1')).fetch(new Request(req,{headers}));
 }
 if(url.pathname.startsWith('/v1/device/')||url.pathname.startsWith('/v1/authorization/')||['/device','/device.js','/device.css'].includes(url.pathname)){
  if(!env.AUTHORIZATION||!env.LOGIN_RATE||!env.CODE_RATE||!env.ACCESS_TEAM_DOMAIN||!env.ACCESS_AUDIENCE||!env.ENROLLMENT_ADMIN||env.ENROLLMENT_ADMIN.length<32)return response({error:'temporarily_unavailable'},503);
  if(url.protocol!=='https:')return response({error:'invalid_request'},400);
  const publicRoute=['/v1/device/code','/v1/device/token','/v1/authorization/request','/v1/authorization/token'].includes(url.pathname);
  let principal;
  if(!publicRoute){try{principal=await accessPrincipal(req,env)}catch{return response({error:'access_denied'},403)}}
  const ip=req.headers.get('CF-Connecting-IP');if(!ip)return response({error:'invalid_request'},400);
  if(!(await env.LOGIN_RATE.limit({key:'ip:'+ip})).success||!(await env.LOGIN_RATE.limit({key:'global:'+url.pathname})).success)return response({error:'temporarily_unavailable'},429);
  if(['/v1/device/code','/v1/authorization/request'].includes(url.pathname)&&!(await env.CODE_RATE.limit({key:ip})).success)return response({error:'temporarily_unavailable'},429);
  if(req.method==='GET'){const asset=deviceAsset(url.pathname);if(asset)return asset}
  if(req.method!=='POST'||!['/v1/device/code','/v1/device/token','/v1/device/review','/v1/device/approve','/v1/authorization/request','/v1/authorization/token'].includes(url.pathname))return response({error:'invalid_request'},404);
  const headers=new Headers(req.headers);headers.delete('X-Hopsesh-Principal');if(principal)headers.set('X-Hopsesh-Principal',principal);
  headers.set('X-Hopsesh-External-Origin',url.origin);
  return env.AUTHORIZATION.get(env.AUTHORIZATION.idFromName('hopsesh-device-enrollment-v1')).fetch(new Request(req,{headers}));
 }
 const space=req.headers.get('X-Hopsesh-Space');if(!opaque(space))return json({error:'space'},400);
 const token=(req.headers.get('authorization')||'').replace(/^Bearer /,'');
 if(!env.ENROLLMENT_ADMIN||env.ENROLLMENT_ADMIN.length<32||token.length>4096)return json({error:'authorization'},403);
 // Authenticate before allocating a Durable Object. Arbitrary unauthenticated
 // namespace headers must not create unbounded paid objects.
 if(['/v1/enrollment/register','/v1/enrollment/check','/v1/enrollment/revoke-device','/v1/operator/stats'].includes(url.pathname)){
  if(!token||await hash(token)!==await hash(env.ENROLLMENT_ADMIN))return json({error:'authorization'},403);
 }else{
  try{
   const {payload}=await jwtVerify(token,encoder.encode(env.ENROLLMENT_ADMIN),{algorithms:['HS256'],issuer:'hopsesh-relay-v1',audience:'hopsesh-relay-mailbox'});
   if(payload.space!==space||!opaque(payload.device)||!['device','cloud-session'].includes(payload.kind))return json({error:'authorization'},403);
   if(['/v1/messages','/v1/ack'].includes(url.pathname)&&env.TRAFFIC_RATE&&(!(await env.TRAFFIC_RATE.limit({key:'device:'+space+':'+payload.device})).success||env.GLOBAL_TRAFFIC_RATE&&!(await env.GLOBAL_TRAFFIC_RATE.limit({key:'global:mailbox'})).success))return json({error:'quota'},429);
   if(url.pathname==='/v1/notifications'){
    if(url.protocol!=='https:'||req.method!=='GET'||url.search||req.headers.get('Upgrade')?.toLowerCase()!=='websocket')return json({error:'websocket-required'},426);
    if(!env.LOGIN_RATE||!(await env.LOGIN_RATE.limit({key:'notifications:'+space+':'+payload.device})).success||!(await env.LOGIN_RATE.limit({key:'global:notifications'})).success)return json({error:'quota'},429);
   }
  }catch{return json({error:'authorization'},403)}
 }
 return env.MAILBOX.get(env.MAILBOX.idFromName(space)).fetch(req);
}};
