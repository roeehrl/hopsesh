// Separate immutable public release downloads; never stores relay credentials.
export default {async fetch(req,env) {
 const url=new URL(req.url);
 if(!['GET','HEAD'].includes(req.method)&&!(req.method==='PUT'&&env.PUBLISH_TOKEN))return new Response(null,{status:405,headers:{Allow:'GET, HEAD'}});
 if(url.search || !/^\/releases\/v0\.5\.\d+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?\/(?:checksums\.txt(?:\.sig)?|hopsesh_[A-Za-z0-9._-]+\.(?:tar\.gz|zip|dmg))$/.test(url.pathname))return new Response(null,{status:404});
 const key=url.pathname.slice(1);
 if(req.method==='PUT'){
  // Publication is separate from relay enrollment and never forwarded. R2's
  // conditional write, rather than a head/put race, prevents overwrites.
  const token=req.headers.get('authorization')||'';
  const digest=async s=>Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(s)))).map(v=>v.toString(16).padStart(2,'0')).join('');
  if(env.PUBLISH_TOKEN.length<32||await digest(token)!==await digest('Bearer '+env.PUBLISH_TOKEN))return new Response(null,{status:403});
  const sum=req.headers.get('X-Hopsesh-SHA256')||'',size=Number(req.headers.get('content-length'));
  if(!/^[a-f0-9]{64}$/.test(sum)||!Number.isSafeInteger(size)||size<1||size>200*1024*1024||!req.body)return new Response(null,{status:400});
  try{
   const result=await env.RELEASES.put(key,req.body,{onlyIf:new Headers({'If-None-Match':'*'}),sha256:sum,customMetadata:{sha256:sum}});
   if(result)return new Response(null,{status:201,headers:{'Cache-Control':'no-store'}});
   const old=await env.RELEASES.head(key);
   return new Response(null,{status:old?.customMetadata?.sha256===sum&&old.size===size?200:409,headers:{'Cache-Control':'no-store'}});
  }catch{return new Response(null,{status:400,headers:{'Cache-Control':'no-store'}})}
 }
 const object=req.method==='HEAD'?await env.RELEASES.head(key):await env.RELEASES.get(key);
 if(!object)return new Response(null,{status:404,headers:{'Cache-Control':'no-store'}});
 const headers=new Headers({'Cache-Control':'public, max-age=31536000, immutable','X-Content-Type-Options':'nosniff','Content-Type':key.endsWith('checksums.txt')?'text/plain; charset=utf-8':'application/octet-stream','Content-Length':String(object.size)});
 if(object.httpEtag)headers.set('ETag',object.httpEtag);
 return new Response(req.method==='HEAD'?null:object.body,{headers});
}};
