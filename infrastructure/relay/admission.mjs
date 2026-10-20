import {authorizationForm,checkPublicIdentity,verifyIdentitySignature,digest,response} from './authorization.mjs';

// One-use provisional routing admission. Claiming a ticket never approves the
// fresh cloud identity on a desktop or grants any device/native receiver method.
export const ADMISSION_LIMITS=Object.freeze({pending:512,perDevice:16,ticket:600,lease:86400});
// A full mailbox needs capacity to be released, not automatic claim retries.
// Keep this distinct from transient gateway throttling (HTTP 429).
export class AdmissionCapacityError extends Error {}
const random=()=>Array.from(crypto.getRandomValues(new Uint8Array(32))).map(v=>v.toString(16).padStart(2,'0')).join('');
const error=(value,status=400)=>response({error:value},status);
const name=value=>typeof value==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(value);
const provider=value=>['claude-hosted','codex-current','codex-legacy','work-cloud'].includes(value);
const signatureMessage=(origin,values)=>'hopsesh-cloud-admission-v1\0'+origin+'\0'+['ticket','identity','provider','session','incarnation','lease_expires'].map(k=>values.get(k)||'').join('\0');

export function createAdmissionHandler(storage,enroll,authorize,revoke,clock=()=>Date.now()) {
 const transaction=async fn=>{try{return await storage.transaction(fn)}catch{return error('temporarily_unavailable',503)}};
 return async req=>{
  const url=new URL(req.url),origin=req.headers.get('X-Hopsesh-External-Origin')||url.origin,now=Math.floor(clock()/1000);
  try{
   if(req.method!=='POST')return error('invalid_request',405);
   let values;try{values=await authorizationForm(req)}catch{return error('invalid_request')}
   if(url.pathname==='/v1/cloud/tickets'){
    const principal=req.headers.get('X-Hopsesh-Principal'),device=req.headers.get('X-Hopsesh-Issuer'),credential=req.headers.get('X-Hopsesh-Credential');
    if(!/^[A-Za-z0-9_-]{16,128}$/.test(principal||'')||!name(device)||! /^[a-f0-9]{64}$/.test(credential||''))return error('access_denied',403);
    const kind=values.get('provider'),session=values.get('session'),ttl=Number(values.get('lease_seconds'));
    if(!provider(kind)||!name(session)||!Number.isInteger(ttl)||ttl<60||ttl>ADMISSION_LIMITS.lease)return error('invalid_request');
    if(!await authorize(principal,device,credential))return error('access_denied',403);
    const secret=random(),key='ticket:'+await digest(secret);
    return await transaction(async tx=>{
     const rows=await tx.list({prefix:'ticket:',limit:ADMISSION_LIMITS.pending});
     if(rows.size>=ADMISSION_LIMITS.pending||[...rows.values()].filter(t=>t.principal===principal&&t.issuer===device).length>=ADMISSION_LIMITS.perDevice)return error('quota',429);
     const state={principal,issuer:device,credential,provider:kind,session,ttl,expires:now+ADMISSION_LIMITS.ticket,status:'pending'};
     await tx.put(key,state);const alarm=await storage.getAlarm();if(!alarm||alarm>state.expires*1000)await storage.setAlarm(state.expires*1000);
     return response({ticket:secret,provider:kind,session,expires:state.expires,lease_seconds:ttl},201);
    });
   }
   const secret=values.get('ticket');if(!/^[a-f0-9]{64}$/.test(secret||''))return error('invalid_grant');
   const key='ticket:'+await digest(secret);
   if(url.pathname==='/v1/cloud/status'){
    const principal=req.headers.get('X-Hopsesh-Principal'),issuer=req.headers.get('X-Hopsesh-Issuer'),credential=req.headers.get('X-Hopsesh-Credential');
    if(!principal||!issuer||!credential||!await authorize(principal,issuer,credential))return error('access_denied',403);
    const state=await storage.get(key);if(!state||state.principal!==principal||state.issuer!==issuer)return error('access_denied',403);
    const status=state.status==='revoked'?'revoked':state.connection ? state.connection.expires>now?'claimed':'expired':state.expires>now?'pending':'expired';
    return response({status,provider:state.provider,session:state.session,expires:state.expires,leaseExpires:state.connection?.expires||0,public:state.public||null});
   }
   if(url.pathname==='/v1/cloud/revoke'){
    const principal=req.headers.get('X-Hopsesh-Principal'),issuer=req.headers.get('X-Hopsesh-Issuer'),credential=req.headers.get('X-Hopsesh-Credential');
    if(!principal||!issuer||!credential||!await authorize(principal,issuer,credential))return error('access_denied',403);
    return await transaction(async tx=>{
     const t=await tx.get(key);if(!t||t.principal!==principal||t.issuer!==issuer)return error('access_denied',403);
     t.status='revoked';await tx.put(key,t);
     if(t.connection){try{await revoke(t.principal,t.connection.device,{device:issuer,credential})}catch{return error('temporarily_unavailable',503)}}
     return response({revoked:true});
    });
   }
   if(url.pathname!=='/v1/cloud/claim')return error('invalid_request',404);
   let publicIdentity;try{publicIdentity=await checkPublicIdentity(values.get('identity'))}catch{return error('invalid_identity')}
   const kind=values.get('provider'),session=values.get('session'),incarnation=values.get('incarnation'),lease=Number(values.get('lease_expires'));
   if(!provider(kind)||!name(session)||! /^[a-f0-9]{32}$/.test(incarnation||'')||publicIdentity.endpoint!=='cloud/'+kind+'/'+incarnation||!Number.isInteger(lease)||lease<=now||lease>now+ADMISSION_LIMITS.lease)return error('invalid_request');
   try{await verifyIdentitySignature(publicIdentity,values.get('proof'),signatureMessage(origin,values))}catch{return error('invalid_proof')}
   return await transaction(async tx=>{
    const state=await tx.get(key);if(!state||state.expires<=now||state.status==='revoked')return error('invalid_grant');
    if(state.provider!==kind||state.session!==session||!await authorize(state.principal,state.issuer,state.credential))return error('access_denied',403);
    const claim=await digest(signatureMessage(origin,values));
    if(state.claim&&state.claim!==claim)return error('already_claimed',409);
    if(!state.connection){
     const ttl=Math.min(state.ttl,lease-now);
     try{state.connection=await enroll(state.principal,publicIdentity.id,ttl,{device:state.issuer,credential:state.credential},await digest(key+'\0'+claim),Math.min(lease,now+state.ttl))}catch(e){return e instanceof AdmissionCapacityError?error('capacity_exceeded',409):error('temporarily_unavailable',503)}
     state.claim=claim;state.status='claimed';state.public=publicIdentity;state.retainUntil=state.connection.expires;
     await tx.put(key,state);
    }
    if(state.connection.expires<=now)return error('expired_token');
    return response({...state.connection,kind:'cloud-session',provisional:true});
   });
  }catch{return error('temporarily_unavailable',503)}
 };
}
export async function maintainAdmission(storage,now=Math.floor(Date.now()/1000)){
 let next=Infinity;
 await storage.transaction(async tx=>{for(const[key,value]of await tx.list({prefix:'ticket:'})){const expiry=value.retainUntil||value.expires;if(expiry<=now)await tx.delete(key);else next=Math.min(next,expiry)}});
 if(Number.isFinite(next)){const alarm=await storage.getAlarm();if(!alarm||alarm>next*1000)await storage.setAlarm(next*1000)}
}
