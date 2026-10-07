// Local-only cross-language matrix fixture. Never included by the Worker entry.
import http from 'node:http';
import https from 'node:https';
import {readFileSync} from 'node:fs';
import {Readable} from 'node:stream';
import {createHandler} from './worker.mjs';
import {createAuthorizationHandler} from './authorization.mjs';
class Storage {
 constructor(){this.data=new Map();this.tail=Promise.resolve()}
 async get(k){return structuredClone(this.data.get(k))}
 async put(k,v){this.data.set(k,structuredClone(v))}
 async delete(k){this.data.delete(k)}
 async list({prefix='',limit=Infinity}={}){return new Map([...this.data].filter(([k])=>k.startsWith(prefix)).sort(([a],[b])=>a.localeCompare(b)).slice(0,limit))}
 async setAlarm(){}
 async getAlarm(){return null}
 async transaction(fn){let release;const prior=this.tail;this.tail=new Promise(r=>release=r);await prior;const tx=new Storage();tx.data=structuredClone(this.data);try{const result=await fn(tx);this.data=tx.data;return result}finally{release()}}
}
const storage=new Map(),blobs=new Map();
const authStorage=new Storage();
const bucket={put:async(k,v)=>blobs.set(k,v),get:async k=>blobs.has(k)?{text:async()=>blobs.get(k)}:null,delete:async k=>blobs.delete(k)};
const handler=async(req,res)=>{
 const space=req.headers['x-hopsesh-space'];if(!storage.has(space))storage.set(space,new Storage());
 try{
  const headers=new Headers();for(const[k,v]of Object.entries(req.headers))if(v)headers.set(k,String(v));
  const request=new Request((tls?'https':'http')+'://'+req.headers.host+req.url,{method:req.method,headers,body:['GET','HEAD'].includes(req.method)?undefined:Readable.toWeb(req),duplex:'half'});
  let response;
  if(req.url.startsWith('/v1/device/')||req.url.startsWith('/v1/authorization/')){
   // Test-only browser substitute. Production always verifies an Access JWT.
   if(req.url.endsWith('/review')||req.url.endsWith('/approve')){
    if(headers.get('Authorization')!=='Bearer fixture-browser-secret'){res.writeHead(403);res.end('{}');return}
    headers.set('X-Hopsesh-Principal','a'.repeat(64));
   }
   const enroll=async(principal,device,ttl)=>{
    if(!storage.has(principal))storage.set(principal,new Storage());
    const r=await createHandler(storage.get(principal),bucket,'fixture-admin-secret-with-32-bytes-minimum',principal)(new Request(request.url.replace(/\/v1\/.*$/,'/v1/enrollment/register'),{method:'POST',headers:{Authorization:'Bearer fixture-admin-secret-with-32-bytes-minimum'},body:JSON.stringify({device,ttl})}));
    if(r.status!==201)throw new Error('fixture enrollment');return r.json();
   };
   response=await createAuthorizationHandler(authStorage,enroll)(new Request(request,{headers}));
  }else response=await createHandler(storage.get(space),bucket,'fixture-admin-secret-with-32-bytes-minimum',space)(request);
  res.writeHead(response.status,Object.fromEntries(response.headers));res.end(Buffer.from(await response.arrayBuffer()));
 }catch{res.writeHead(500);res.end('{}')}
};
const tls=process.env.HOPSESH_RELAY_FIXTURE_CERT && process.env.HOPSESH_RELAY_FIXTURE_KEY;
const server=tls?https.createServer({cert:readFileSync(process.env.HOPSESH_RELAY_FIXTURE_CERT),key:readFileSync(process.env.HOPSESH_RELAY_FIXTURE_KEY)},handler):http.createServer(handler);
server.listen(0,'127.0.0.1',()=>console.log(JSON.stringify({url:(tls?'https':'http')+'://127.0.0.1:'+server.address().port})));
