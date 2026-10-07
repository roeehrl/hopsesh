import {SignJWT, jwtVerify} from 'jose';
// Experimental Hopsesh-only infrastructure. Payloads stay encrypted end to end.
const encoder = new TextEncoder();
export const LIMITS = Object.freeze({ frame: 24 * 1024 * 1024, bytes: 64 * 1024 * 1024, messages: 1000, devices: 32, lifetime: 86400, batch: 1 });
const opaque = s => typeof s === 'string' && /^[A-Za-z0-9_-]{16,128}$/.test(s);
const hash = async s => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(s)))).map(v => v.toString(16).padStart(2,'0')).join('');
const json = (v,status=200) => new Response(JSON.stringify(v),{status,headers:{'Content-Type':'application/json','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'}});
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
export function createHandler(storage,bucket,adminToken,space,clock=()=>Date.now()) {
 return async req => {
  const url=new URL(req.url),path=url.pathname,now=Math.floor(clock()/1000);
  try {
   if(path==='/v1/capabilities'&&req.method==='GET')return json({protocol:1,limits:LIMITS,methods:['GET','POST'],experimental:true});
   if(!opaque(space))return json({error:'space'},400);
   const token=(req.headers.get('authorization')||'').replace(/^Bearer /,'');
   if(path==='/v1/enrollment/register'){
    if(req.method!=='POST')return json({error:'method'},405);
    // Operator authorization only enrolls a routing credential. Local fingerprint
    // approval independently grants peer actions; this endpoint cannot widen it.
    if(!adminToken||!token||await hash(token)!==await hash(adminToken))return json({error:'authorization'},403);
    const body=await bounded(req,4096);
    if(!opaque(body.device)||!Number.isInteger(body.ttl)||body.ttl<60||body.ttl>LIMITS.lifetime)return json({error:'enrollment'},400);
    const secret=Array.from(crypto.getRandomValues(new Uint8Array(32))).map(v=>v.toString(16).padStart(2,'0')).join('');
    const credential=await new SignJWT({space,device:body.device,nonce:secret}).setProtectedHeader({alg:'HS256',typ:'JWT'}).setIssuer('hopsesh-relay-v1').setAudience('hopsesh-relay-mailbox').setIssuedAt(now).setExpirationTime(now+body.ttl).sign(encoder.encode(adminToken));
    const key=await hash(credential);
    return await storage.transaction(async tx=>{
     const count=(await tx.get('device-count'))||0;
     const old=await tx.get('device:'+body.device);
     if(!old&&count>=LIMITS.devices)return json({error:'quota'},429);
     if(old)await tx.delete('token:'+old.token);
     await tx.put('device:'+body.device,{token:key,expires:now+body.ttl,revoked:false});
     await tx.put('token:'+key,body.device);
     if(!old)await tx.put('device-count',count+1);
     return json({token:credential,space,device:body.device,expires:now+body.ttl},201);
    });
   }
   const tokenHash=await hash(token);
   const device=await storage.get('token:'+tokenHash);
   const grant=device&&await storage.get('device:'+device);
   if(!grant||grant.revoked||grant.expires<=now||grant.token!==tokenHash)return json({error:'authorization'},403);
   if(path==='/v1/enrollment/revoke'){
    if(req.method!=='POST')return json({error:'method'},405);
    // A scoped credential may revoke itself, never another endpoint.
    return await storage.transaction(async tx=>{await tx.put('device:'+device,{...grant,revoked:true});await tx.delete('token:'+tokenHash);return json({revoked:true})});
   }
   if(path==='/v1/messages'&&req.method==='POST'){
    const e=await bounded(req,LIMITS.frame);
    if(!['request','response'].includes(e.kind)||e.protocol!==1||e.space!==space||e.from!==device||!opaque(e.to)||e.to===device||!opaque(e.id)||!opaque(e.operation)||!Number.isInteger(e.created)||!Number.isInteger(e.expires)||e.expires<=now||e.created>now+60||e.expires<=e.created||e.expires-e.created>LIMITS.lifetime||typeof e.ciphertext!=='string'||typeof e.signature!=='string'||!e.ciphertext.length||e.signature.length!==88)return json({error:'envelope'},400);
    const target=await storage.get('device:'+e.to);if(!target||target.revoked||target.expires<=now)return json({error:'recipient'},403);
    const data=JSON.stringify(e),sum=await hash(data),key=space+'/'+e.to+'/'+e.id;
    return await storage.transaction(async tx=>{
     const fresh=await tx.get('device:'+device),receiver=await tx.get('device:'+e.to);
     if(!fresh||fresh.revoked||fresh.expires<=now||fresh.token!==tokenHash||!receiver||receiver.revoked||receiver.expires<=now)return json({error:'authorization'},403);
     const prior=await tx.get('id:'+device+':'+e.id);
     if(prior)return prior.sum===sum?json({sequence:prior.sequence,duplicate:true}):json({error:'id-conflict'},409);
     const q=(await tx.get('quota:'+e.to))||{bytes:0,count:0};
     const tombstones=(await tx.get('id-count'))||0;
     if(q.bytes+data.length>LIMITS.bytes||q.count>=LIMITS.messages||tombstones>=10000)return json({error:'quota'},429);
     const sequence=((await tx.get('sequence:'+e.to))||0)+1;
     // If bucket publication or the transaction fails, no delivery is visible.
     // Unreferenced ciphertext has a separate R2 lifecycle cleanup policy.
     await bucket.put(key,data,{customMetadata:{expires:String(e.expires)}});
     await tx.put('message:'+e.to+':'+String(sequence).padStart(20,'0'),{key,bytes:data.length,expires:e.expires,sequence});
     await tx.put('id:'+device+':'+e.id,{sum,sequence});await tx.put('id-count',tombstones+1);
     await tx.put('sequence:'+e.to,sequence);await tx.put('quota:'+e.to,{bytes:q.bytes+data.length,count:q.count+1});
     await storage.setAlarm(clock()+60000);
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
  }catch(e){return json({error:e?.message==='too-large'?'frame':'invalid-request'},e?.message==='too-large'?413:400)}
 };
}
async function removeMessages(storage,bucket,device,predicate){
 await storage.transaction(async tx=>{
  const rows=await tx.list({prefix:'message:'+device+':' });const q=(await tx.get('quota:'+device))||{bytes:0,count:0};
  for(const [k,m]of rows){if(!predicate(m))continue;await bucket.delete(m.key);await tx.delete(k);q.bytes-=m.bytes;q.count--;}
  await tx.put('quota:'+device,q);
 });
}
export class Mailbox {
 constructor(ctx,env){this.ctx=ctx;this.env=env}
 async fetch(req){const space=req.headers.get('X-Hopsesh-Space');return createHandler(this.ctx.storage,this.env.CIPHERTEXT,this.env.ENROLLMENT_ADMIN,space)(req)}
 async alarm(){
  const now=Math.floor(Date.now()/1000);const devices=await this.ctx.storage.list({prefix:'device:'});
  for(const [key]of devices)await removeMessages(this.ctx.storage,this.env.CIPHERTEXT,key.slice(7),m=>m.expires<=now);
  const pending=await this.ctx.storage.list({prefix:'message:',limit:1});if(pending.size)await this.ctx.storage.setAlarm(Date.now()+60000);
 }
}
export default {async fetch(req,env){
 const url=new URL(req.url);
 if(url.pathname==='/v1/capabilities'&&req.method==='GET')return json({protocol:1,limits:LIMITS,experimental:true});
 const space=req.headers.get('X-Hopsesh-Space');if(!opaque(space))return json({error:'space'},400);
 const token=(req.headers.get('authorization')||'').replace(/^Bearer /,'');
 if(!env.ENROLLMENT_ADMIN||env.ENROLLMENT_ADMIN.length<32||token.length>4096)return json({error:'authorization'},403);
 // Authenticate before allocating a Durable Object. Arbitrary unauthenticated
 // namespace headers must not create unbounded paid objects.
 if(url.pathname==='/v1/enrollment/register'){
  if(!token||await hash(token)!==await hash(env.ENROLLMENT_ADMIN))return json({error:'authorization'},403);
 }else{
  try{
   const {payload}=await jwtVerify(token,encoder.encode(env.ENROLLMENT_ADMIN),{algorithms:['HS256'],issuer:'hopsesh-relay-v1',audience:'hopsesh-relay-mailbox'});
   if(payload.space!==space||!opaque(payload.device))return json({error:'authorization'},403);
  }catch{return json({error:'authorization'},403)}
 }
 return env.MAILBOX.get(env.MAILBOX.idFromName(space)).fetch(req);
}};
