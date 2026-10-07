import test from 'node:test';
import assert from 'node:assert/strict';
import {generateKeyPairSync,sign} from 'node:crypto';
import worker,{Authorization} from './worker.mjs';
import {createAuthorizationHandler,maintainAuthorization,digest,AUTH_LIMITS} from './authorization.mjs';
import {deviceAsset} from './device-page.mjs';
import {generateKeyPair,exportJWK,SignJWT} from 'jose';
import {accessPrincipal} from './authorization.mjs';
class Storage {
 constructor(){this.values=new Map();this.alarm=null;this.tail=Promise.resolve()}
 async get(k){return structuredClone(this.values.get(k))}
 async put(k,v){this.values.set(k,structuredClone(v))}
 async delete(k){this.values.delete(k)}
 async list({prefix='',limit=Infinity}={}){return new Map([...this.values].filter(([k])=>k.startsWith(prefix)).sort(([a],[b])=>a.localeCompare(b)).slice(0,limit))}
 async getAlarm(){return this.alarm}
 async setAlarm(t){this.alarm=t}
 async transaction(fn){let release;const before=this.tail;this.tail=new Promise(r=>release=r);await before;const tx=new Storage();tx.values=structuredClone(this.values);try{const result=await fn(tx);this.values=tx.values;return result}finally{release()}}
}
async function fixture(){
 const storage=new Storage(),principal='a'.repeat(64);let clock=Date.now(),enrollments=0;
 const enroll=async(space,device)=>{enrollments++;return {space,device,token:'routing-secret-only',expires:Math.floor(clock/1000)+86400}};
 const handle=createAuthorizationHandler(storage,enroll,()=>clock);
 const invoke=(path,body,headers={})=>handle(new Request('https://relay.test'+path,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded',...headers},body:new URLSearchParams(body)}));
 const browser=(path,body,who=principal,extra={})=>invoke(path,body,{'X-Hopsesh-Principal':who,Origin:'https://relay.test','X-Hopsesh-CSRF':'review',...extra});
 const {publicKey,privateKey}=generateKeyPairSync('ed25519');
 const signing=publicKey.export({format:'der',type:'spki'}).subarray(-32),recipient='age1'+'q'.repeat(58),endpoint='test-native-machine';
 const id=await digest(Buffer.concat([Buffer.from('hopsesh-relay-identity-v1\0'),signing,Buffer.from(recipient+'\0'+endpoint)]));
 const identity=JSON.stringify({endpoint,id,signing:signing.toString('base64'),recipient}),nonce='d'.repeat(32),proof=sign(null,Buffer.from('hopsesh-relay-login-v1\0https://relay.test\0'+nonce+'\0'+identity+'\0hopsesh-headless-v1\0relay.routing\0\0\0'),privateKey).toString('base64');
 const args={client_id:'hopsesh-headless-v1',scope:'relay.routing',identity,nonce,proof};
 const issue=async()=>{const r=await invoke('/v1/device/code',args);assert.equal(r.status,200);return r.json()};
 const token=flow=>invoke('/v1/device/token',{client_id:'hopsesh-headless-v1',grant_type:'urn:ietf:params:oauth:grant-type:device_code',device_code:flow.device_code});
 const signedArgs=extra=>{const body={...args,...extra};const message='hopsesh-relay-login-v1\0https://relay.test\0'+nonce+'\0'+identity+'\0'+['client_id','scope','redirect_uri','code_challenge','state'].map(k=>body[k]||'').join('\0');body.proof=sign(null,Buffer.from(message),privateKey).toString('base64');return body};
 return {storage,principal,invoke,browser,issue,token,args,signedArgs,id,advance:s=>clock+=s*1000,now:()=>Math.floor(clock/1000),enrollments:()=>enrollments};
}
test('signed device request, browser review and approval create only one routing credential',async()=>{
 const f=await fixture(),flow=await f.issue();assert.equal(flow.verification_uri,'https://relay.test/device');
 assert.equal(flow.interval,5);assert.equal(flow.expires_in,600);
 assert.ok(!JSON.stringify([...f.storage.values]).includes(flow.device_code),'raw device code must not be stored');
 f.advance(5);let r=await f.token(flow);assert.equal((await r.json()).error,'authorization_pending');
 r=await f.browser('/v1/device/review',{user_code:flow.user_code});assert.equal(r.status,200);const review=await r.json();
 assert.equal(review.device,f.id);assert.equal(review.scope,'relay.routing');assert.ok(!JSON.stringify(review).includes(flow.device_code));
 r=await f.browser('/v1/device/approve',{user_code:flow.user_code,nonce:review.nonce,decision:'approve'});assert.equal(r.status,200);assert.equal(f.enrollments(),0);
 f.advance(5);r=await f.token(flow);assert.equal(r.status,200);const grant=await r.json();
 assert.equal(grant.device,f.id);assert.equal(grant.space,f.principal);assert.equal(grant.scope,'relay.routing');assert.equal(grant.expires_in,86400);assert.equal(f.enrollments(),1);
 f.advance(5);assert.deepEqual(await(await f.token(flow)).json(),{...grant,expires_in:86395});assert.equal(f.enrollments(),1,'lost response retry rotated credential');
 assert.equal((await f.browser('/v1/device/approve',{user_code:flow.user_code,nonce:review.nonce,decision:'approve'})).status,400);
});
test('slow_down persists increased pacing, denial ends flow, expiry reclaims bounded slots',async()=>{
 const f=await fixture(),flow=await f.issue();
 assert.equal((await(await f.token(flow)).json()).error,'slow_down');
 f.advance(5);assert.equal((await(await f.token(flow)).json()).error,'slow_down');
 f.advance(15);assert.equal((await(await f.token(flow)).json()).error,'authorization_pending');
 const review=await(await f.browser('/v1/device/review',{user_code:flow.user_code})).json();
 await f.browser('/v1/device/approve',{user_code:flow.user_code,nonce:review.nonce,decision:'deny'});
 assert.equal((await(await f.token(flow)).json()).error,'access_denied');assert.equal(f.enrollments(),0);
 f.advance(601);assert.equal((await(await f.token(flow)).json()).error,'expired_token');
 await maintainAuthorization(f.storage,f.now());assert.equal((await f.storage.list({prefix:'request:'})).size,0);assert.equal((await f.storage.list({prefix:'user:'})).size,0);
});
test('cross-account, cross-origin and missing review nonce cannot approve a request',async()=>{
 const f=await fixture(),flow=await f.issue(),body={user_code:flow.user_code};
 assert.equal((await f.invoke('/v1/device/review',body)).status,403);
 assert.equal((await f.browser('/v1/device/review',body,f.principal,{Origin:'https://attacker.test'})).status,403);
 assert.equal((await f.browser('/v1/device/review',body,f.principal,{'X-Hopsesh-CSRF':''})).status,403);
 const review=await(await f.browser('/v1/device/review',body)).json();
 for(const [who,nonce] of [['b'.repeat(64),review.nonce],[f.principal,'forged']])assert.equal((await f.browser('/v1/device/approve',{...body,nonce,decision:'approve'},who)).status,403);
 assert.equal(f.enrollments(),0);
});
test('forged proof, cloud identity, wrong scope and duplicated OAuth fields are refused before allocation',async()=>{
 const f=await fixture();
 for(const args of [{...f.args,proof:Buffer.alloc(64).toString('base64')},{...f.args,scope:'relay.admin'},{...f.args,identity:f.args.identity.replace('test-native-machine','cloud/claude-hosted/123')}])assert.equal((await f.invoke('/v1/device/code',args)).status,400);
 const req=new Request('https://relay.test/v1/device/code',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(f.args).toString()+'&client_id=other'});
 assert.equal((await createAuthorizationHandler(f.storage,()=>assert.fail())(req)).status,400);assert.equal(f.storage.values.size,0);
 for(let i=0;i<AUTH_LIMITS.pending;i++)await f.storage.put('request:'+i,{expires:f.now()+600});
 assert.equal((await f.invoke('/v1/device/code',f.args)).status,429);
});
test('Worker rate limits and Access verification precede the single paid authorization object',async()=>{
 let objects=0;const env={ENROLLMENT_ADMIN:'operator-secret-with-at-least-32-bytes',ACCESS_TEAM_DOMAIN:'https://test.cloudflareaccess.com',ACCESS_AUDIENCE:'aud',LOGIN_RATE:{limit:async()=>({success:false})},CODE_RATE:{limit:async()=>({success:true})},AUTHORIZATION:{idFromName(){objects++;return 'fixed'},get(){assert.fail()}}};
 const request=(path,headers={})=>new Request('https://relay.test'+path,{method:'POST',headers:{'CF-Connecting-IP':'192.0.2.1',...headers},body:''});
 assert.equal((await worker.fetch(request('/v1/device/code'),env)).status,429);assert.equal(objects,0);
 assert.equal((await worker.fetch(request('/v1/device/approve',{'X-Hopsesh-Principal':'a'.repeat(64),'cf-access-jwt-assertion':'forged'}),env)).status,403);assert.equal(objects,0);
 assert.equal((await worker.fetch(request('/v1/device/code'),{...env,LOGIN_RATE:undefined})).status,503);assert.equal(objects,0);
});
test('verification page has no automatic approval, inline scripts or credential outputs',async()=>{
 const r=deviceAsset('/device'),html=await r.text();
 assert.match(r.headers.get('Content-Security-Policy'),/frame-ancestors 'none'/);assert.match(html,/Compare the code and full fingerprint/);
 assert.ok(!html.includes('device_code'));assert.ok(!html.includes('onclick='));assert.ok(!html.includes('<script>'));
 assert.equal(r.headers.get('Cache-Control'),'no-store');
});
test('Authorization object uses account-owned mailbox without exposing operator token',async()=>{
 const storage=new Storage(),seen=[];
 const obj=new Authorization({storage},{ENROLLMENT_ADMIN:'operator-secret',MAILBOX:{idFromName(s){seen.push(s);return s},get(){return {fetch:async req=>{seen.push(req.headers.get('Authorization'));return new Response(JSON.stringify({space:'a'.repeat(64),token:'scoped',expires:Math.floor(Date.now()/1000)+3600}),{status:201})}}}}});
 const f=await fixture();const req=new Request('https://relay.test/v1/device/code',{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(f.args)});
 assert.equal((await obj.fetch(req)).status,200);assert.deepEqual(seen,[],'issuing a code must not enroll a mailbox');
});

test('desktop authorization requires S256, exact loopback redirect and verifier; retries do not re-enroll',async()=>{
 const f=await fixture(),verifier='v'.repeat(64),challenge=Buffer.from(await crypto.subtle.digest('SHA-256',Buffer.from(verifier))).toString('base64url');
 const args=f.signedArgs({client_id:'hopsesh-desktop-v1',response_type:'code',redirect_uri:'http://127.0.0.1:43210/callback',code_challenge_method:'S256',code_challenge:challenge,state:'c'.repeat(64)});
 for(const changes of [{redirect_uri:'http://attacker.test:43210/callback'},{redirect_uri:'http://localhost:43210/callback'},{redirect_uri:'http://127.0.0.1:43210/other'},{code_challenge_method:'plain'}])assert.equal((await f.invoke('/v1/authorization/request',f.signedArgs({...args,...changes}))).status,400);
 assert.equal((await f.invoke('/v1/authorization/request',{...args,state:'d'.repeat(64)})).status,400,'proof did not bind PKCE state');
 const r=await f.invoke('/v1/authorization/request',args);assert.equal(r.status,200);const flow=await r.json();assert.ok(!flow.device_code);
 const review=await(await f.browser('/v1/device/review',{user_code:flow.user_code})).json();
 const approval=await(await f.browser('/v1/device/approve',{user_code:flow.user_code,nonce:review.nonce,decision:'approve'})).json();
 const redirect=new URL(approval.redirect_uri);assert.equal(redirect.origin,'http://127.0.0.1:43210');assert.equal(redirect.searchParams.get('state'),args.state);assert.ok(!approval.access_token);
 const exchange={client_id:'hopsesh-desktop-v1',grant_type:'authorization_code',code:redirect.searchParams.get('code'),redirect_uri:args.redirect_uri,code_verifier:verifier};
 assert.equal((await f.invoke('/v1/authorization/token',{...exchange,code_verifier:'w'.repeat(64)})).status,400);
 assert.equal((await f.invoke('/v1/authorization/token',{...exchange,redirect_uri:'http://127.0.0.1:12345/callback'})).status,400);assert.equal(f.enrollments(),0);
 assert.equal((await f.invoke('/v1/authorization/token',exchange)).status,200);assert.equal((await f.invoke('/v1/authorization/token',exchange)).status,200);assert.equal(f.enrollments(),1);
 f.advance(61);assert.equal((await f.invoke('/v1/authorization/token',exchange)).status,400);f.advance(600);await maintainAuthorization(f.storage,f.now());assert.equal((await f.storage.list({prefix:'code:'})).size,0);
});

test('Access verifies signature, issuer, audience, expiry and app type; account hash is independent of email',async()=>{
 const {publicKey,privateKey}=await generateKeyPair('RS256'),key={...await exportJWK(publicKey),kid:'test-authorization-key',alg:'RS256',use:'sig'};
 const team='https://auth-test.cloudflareaccess.com',aud='test-app-audience',original=globalThis.fetch;
 globalThis.fetch=async url=>{assert.equal(String(url),team+'/cdn-cgi/access/certs');return new Response(JSON.stringify({keys:[key]}),{headers:{'Content-Type':'application/json'}})};
 const jwt=async changes=>new SignJWT({type:'app',email:'demo@example.invalid',...changes}).setProtectedHeader({alg:'RS256',kid:key.kid}).setIssuer(team).setAudience(aud).setSubject('subject-fixture').setIssuedAt().setExpirationTime('5m').sign(privateKey);
 const env={ACCESS_TEAM_DOMAIN:team,ACCESS_AUDIENCE:aud};
 const request=token=>new Request('https://relay.test/device',{headers:{'cf-access-jwt-assertion':token,'X-Hopsesh-Principal':'forged'}});
 try{
  const principal=await accessPrincipal(request(await jwt({})),env);assert.equal(principal,await digest(team+'\0subject-fixture'));
  assert.equal(await accessPrincipal(request(await jwt({email:'changed@example.invalid'})),env),principal);
  await assert.rejects(async()=>accessPrincipal(request(await jwt({type:'service_token'})),env));
  await assert.rejects(async()=>accessPrincipal(request(await jwt({})),{...env,ACCESS_AUDIENCE:'other-audience'}));
  const expired=await new SignJWT({type:'app'}).setProtectedHeader({alg:'RS256',kid:key.kid}).setIssuer(team).setAudience(aud).setSubject('subject-fixture').setIssuedAt().setExpirationTime(1).sign(privateKey);
  await assert.rejects(()=>accessPrincipal(request(expired),env));
  await assert.rejects(()=>accessPrincipal(request('forged'),env));
 }finally{globalThis.fetch=original}
});
