import {createRemoteJWKSet, jwtVerify} from 'jose';

// Public device enrollment grants routing only. The independently compared,
// locally pinned peer identity is still required for every native action.
export const AUTH_LIMITS = Object.freeze({pending:512, lifetime:600, interval:5, routing:86400});
const encoder = new TextEncoder();
export const digest = async value => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', typeof value === 'string' ? encoder.encode(value) : value))).map(v=>v.toString(16).padStart(2,'0')).join('');
const random = n => Array.from(crypto.getRandomValues(new Uint8Array(n))).map(v=>v.toString(16).padStart(2,'0')).join('');
export const response = (body,status=200) => new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer'}});
const failure = (error,status=400) => response({error},status);
export async function authorizationForm(req) {
 if(!(req.headers.get('content-type')||'').startsWith('application/x-www-form-urlencoded'))throw new Error('invalid_request');
 const reader=req.body?.getReader();if(!reader)throw new Error('invalid_request');
 const parts=[];let bytes=0;
 for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>8192){await reader.cancel();throw new Error('invalid_request')}parts.push(value)}
 const body=new Uint8Array(bytes);let offset=0;for(const p of parts){body.set(p,offset);offset+=p.length}
 const values=new URLSearchParams(new TextDecoder('utf-8',{fatal:true}).decode(body));
 for(const key of values.keys())if(values.getAll(key).length!==1)throw new Error('invalid_request');
 return values;
}
const b64 = value => {
 if(typeof value!=='string'||! /^[A-Za-z0-9+/]+={0,2}$/.test(value))throw new Error('invalid_request');
 return Uint8Array.from(atob(value),v=>v.charCodeAt(0));
};
const base64url = value => btoa(String.fromCharCode(...value)).replaceAll('+','-').replaceAll('/','_').replaceAll('=','');
const proofMessage = (values,origin,raw) => 'hopsesh-relay-login-v1\0'+origin+'\0'+values.get('nonce')+'\0'+raw+'\0'+['client_id','scope','redirect_uri','code_challenge','state'].map(k=>values.get(k)||'').join('\0');
function nativeRedirect(value){
 const u=new URL(value);if(u.protocol!=='http:'||!['127.0.0.1','[::1]'].includes(u.hostname)||!u.port||u.pathname!=='/callback'||u.username||u.password||u.search||u.hash)throw new Error('invalid_request');return u.toString();
}
export async function checkPublicIdentity(raw) {
 if(!raw||raw.length>2048)throw new Error('invalid_request');
 const p=JSON.parse(raw),signing=b64(p.signing);
 if(signing.length!==32||! /^[a-f0-9]{64}$/.test(p.id||'')||typeof p.endpoint!=='string'||p.endpoint.length<1||p.endpoint.length>256||/[\x00-\x1f\x7f]/.test(p.endpoint)||! /^age1[023456789acdefghjklmnpqrstuvwxyz]{58}$/.test(p.recipient||''))throw new Error('invalid_request');
 const prefix=encoder.encode('hopsesh-relay-identity-v1\0'),suffix=encoder.encode(p.recipient+'\0'+p.endpoint),material=new Uint8Array(prefix.length+signing.length+suffix.length);
 material.set(prefix);material.set(signing,prefix.length);material.set(suffix,prefix.length+signing.length);
 if(await digest(material)!==p.id)throw new Error('invalid_request');
 return p;
}
export async function verifyIdentitySignature(publicIdentity,proof,message){
 const signing=b64(publicIdentity.signing);
 const key=await crypto.subtle.importKey('raw',signing,{name:'Ed25519'},false,['verify']);
 if(!await crypto.subtle.verify('Ed25519',key,b64(proof),encoder.encode(message)))throw new Error('invalid_request');
}
async function identityProof(values,origin) {
 const raw=values.get('identity'),nonce=values.get('nonce');
 if(! /^[a-f0-9]{32}$/.test(nonce||''))throw new Error('invalid_request');
 const p=await checkPublicIdentity(raw);
 if(p.endpoint.startsWith('cloud/'))throw new Error('invalid_request');
 await verifyIdentitySignature(p,values.get('proof'),proofMessage(values,origin,raw));
 return {id:p.id,endpoint:p.endpoint};
}

// Caller authenticates the browser and supplies only an opaque issuer/subject
// hash. Device codes are stored as hashes and never appear in browser responses.
export function createAuthorizationHandler(storage,enroll,clock=()=>Date.now()) {
 return async req => {
  const url=new URL(req.url),origin=req.headers.get('X-Hopsesh-External-Origin')||url.origin,path=url.pathname,now=Math.floor(clock()/1000);
  try {
   if(req.method!=='POST')return failure('invalid_request',405);
   const values=await authorizationForm(req);
   if(path==='/v1/device/code'||path==='/v1/authorization/request') {
    const native=path==='/v1/authorization/request';
    if(values.get('client_id')!==(native?'hopsesh-desktop-v1':'hopsesh-headless-v1')||values.get('scope')!=='relay.routing')return failure('invalid_scope');
    let browser;
    if(native){if(values.get('response_type')!=='code'||values.get('code_challenge_method')!=='S256'||! /^[A-Za-z0-9_-]{43}$/.test(values.get('code_challenge')||'')||! /^[a-f0-9]{64}$/.test(values.get('state')||''))return failure('invalid_request');browser={redirect:nativeRedirect(values.get('redirect_uri')),challenge:values.get('code_challenge'),state:values.get('state')}}
    const identity=await identityProof(values,origin);
    const deviceCode=random(32),key='request:'+await digest(deviceCode),userCode=random(5).toUpperCase().replace(/(.{5})(.{5})/,'$1-$2');
    return await storage.transaction(async tx=>{
     if((await tx.list({prefix:'request:',limit:AUTH_LIMITS.pending})).size>=AUTH_LIMITS.pending)return failure('temporarily_unavailable',429);
     if(await tx.get('user:'+userCode))return failure('temporarily_unavailable',503);
     await tx.put(key,{identity,userCode,expires:now+AUTH_LIMITS.lifetime,interval:AUTH_LIMITS.interval,next:now+AUTH_LIMITS.interval,status:'pending',browser});
     await tx.put('user:'+userCode,key);
     const alarm=await storage.getAlarm();if(!alarm||alarm>(now+AUTH_LIMITS.lifetime)*1000)await storage.setAlarm((now+AUTH_LIMITS.lifetime)*1000);
     if(native)return response({authorization_uri:origin+'/device?user_code='+userCode,user_code:userCode,expires_in:AUTH_LIMITS.lifetime});
     return response({device_code:deviceCode,user_code:userCode,verification_uri:origin+'/device',expires_in:AUTH_LIMITS.lifetime,interval:AUTH_LIMITS.interval});
    });
   }
   if(path==='/v1/device/token') {
    if(values.get('client_id')!=='hopsesh-headless-v1'||values.get('grant_type')!=='urn:ietf:params:oauth:grant-type:device_code')return failure('unsupported_grant_type');
    const code=values.get('device_code');if(! /^[a-f0-9]{64}$/.test(code||''))return failure('invalid_grant');
    const key='request:'+await digest(code);
    return await storage.transaction(async tx=>{
     const state=await tx.get(key);if(!state)return failure('invalid_grant');
     if(state.browser)return failure('invalid_grant');
     if(state.expires<=now)return failure('expired_token');
     if(state.status==='denied')return failure('access_denied');
     if(now<state.next){state.interval+=5;state.next=now+state.interval;await tx.put(key,state);return failure('slow_down')}
     state.next=now+state.interval;
     if(state.status==='pending'){await tx.put(key,state);return failure('authorization_pending')}
     // A lost HTTPS response can retrieve the same scoped credential for the
     // remainder of this short code lease. It never rotates a second credential.
     if(!state.connection){try{state.connection=await enroll(state.principal,state.identity.id,AUTH_LIMITS.routing)}catch{return failure('temporarily_unavailable',503)}}
     await tx.put(key,state);
     return response({access_token:state.connection.token,token_type:'Bearer',expires_in:Math.max(0,state.connection.expires-now),space:state.connection.space,device:state.identity.id,expires:state.connection.expires,scope:'relay.routing'});
    });
   }
   if(path==='/v1/authorization/token') {
    if(values.get('client_id')!=='hopsesh-desktop-v1'||values.get('grant_type')!=='authorization_code')return failure('unsupported_grant_type');
    const code=values.get('code'),verifier=values.get('code_verifier');
    if(! /^[a-f0-9]{64}$/.test(code||'')||! /^[A-Za-z0-9._~-]{43,128}$/.test(verifier||''))return failure('invalid_grant');
    const challenge=base64url(new Uint8Array(await crypto.subtle.digest('SHA-256',encoder.encode(verifier))));
    return await storage.transaction(async tx=>{
     const key=await tx.get('code:'+await digest(code)),state=key&&await tx.get(key);
     if(!state||!state.browser||state.status!=='approved'||state.expires<=now||state.codeExpires<=now||state.browser.challenge!==challenge||state.browser.redirect!==values.get('redirect_uri'))return failure('invalid_grant');
     if(!state.connection){try{state.connection=await enroll(state.principal,state.identity.id,AUTH_LIMITS.routing)}catch{return failure('temporarily_unavailable',503)}}
     await tx.put(key,state);
     return response({access_token:state.connection.token,token_type:'Bearer',expires_in:Math.max(0,state.connection.expires-now),space:state.connection.space,device:state.identity.id,expires:state.connection.expires,scope:'relay.routing'});
    });
   }
   const principal=req.headers.get('X-Hopsesh-Principal');
   if(! /^[a-f0-9]{64}$/.test(principal||'')||req.headers.get('Origin')!==origin||req.headers.get('X-Hopsesh-CSRF')!=='review')return failure('access_denied',403);
   const userCode=values.get('user_code');if(! /^[A-F0-9]{5}-[A-F0-9]{5}$/.test(userCode||''))return failure('invalid_request');
   return await storage.transaction(async tx=>{
    const key=await tx.get('user:'+userCode),state=key&&await tx.get(key);
    if(!state||state.expires<=now||state.status!=='pending')return failure('invalid_grant');
    if(path==='/v1/device/review') {
     // One review nonce per account, without retaining account email or tokens.
     const nonce=random(32);state.review={principal,nonce};await tx.put(key,state);
     return response({user_code:userCode,device:state.identity.id,endpoint:state.identity.endpoint,nonce,expires_in:state.expires-now,scope:'relay.routing'});
    }
    if(path!=='/v1/device/approve')return failure('invalid_request',404);
    if(!state.review||state.review.principal!==principal||state.review.nonce!==values.get('nonce'))return failure('access_denied',403);
    const decision=values.get('decision');if(!['approve','deny'].includes(decision))return failure('invalid_request');
    state.status=decision==='approve'?'approved':'denied';state.principal=principal;delete state.review;
    let redirect;
    if(state.browser){
     redirect=new URL(state.browser.redirect);redirect.searchParams.set('state',state.browser.state);
     if(decision==='approve'){const code=random(32);state.codeExpires=Math.min(state.expires,now+60);state.codeKey='code:'+await digest(code);await tx.put(state.codeKey,key);redirect.searchParams.set('code',code)}else redirect.searchParams.set('error','access_denied');
    }
    await tx.put(key,state);return response({approved:decision==='approve',...(redirect?{redirect_uri:redirect.toString()}:{})});
   });
  }catch{return failure('invalid_request')}
 };
}
export async function maintainAuthorization(storage,now=Math.floor(Date.now()/1000)) {
 let next=Infinity;
 await storage.transaction(async tx=>{
  for(const[key,state]of await tx.list({prefix:'request:'})){
   if(state.expires<=now){await tx.delete('user:'+state.userCode);if(state.codeKey)await tx.delete(state.codeKey);await tx.delete(key)}else next=Math.min(next,state.expires);
  }
 });
 if(Number.isFinite(next))await storage.setAlarm(next*1000);
}

const jwks=new Map();
export async function accessPrincipal(req,env) {
 const team=env.ACCESS_TEAM_DOMAIN,aud=env.ACCESS_AUDIENCE,token=req.headers.get('cf-access-jwt-assertion');
 if(! /^https:\/\/[a-z0-9-]+\.cloudflareaccess\.com$/.test(team||'')||typeof aud!=='string'||!aud||aud.length>256||!token||token.length>8192)throw new Error('authorization');
 if(!jwks.has(team)){if(jwks.size>=4)jwks.clear();jwks.set(team,createRemoteJWKSet(new URL(team+'/cdn-cgi/access/certs')))}
 const {payload}=await jwtVerify(token,jwks.get(team),{algorithms:['RS256'],issuer:team,audience:aud,requiredClaims:['sub','exp','iat']});
 if(typeof payload.sub!=='string'||!payload.sub||payload.sub.length>256||payload.type!=='app')throw new Error('authorization');
 return digest(team+'\0'+payload.sub);
}
