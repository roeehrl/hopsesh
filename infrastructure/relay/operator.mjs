import {fileURLToPath} from 'node:url';

// Read a private operator credential from stdin, never command arguments or UI.
// The output contains allowlisted counters, never routing identities or payloads.
export async function operatorStats(input,fetcher=fetch){
 let origin;try{origin=new URL(input.origin)}catch{throw Error('invalid operator origin')}
 if(origin.protocol!=='https:'||origin.pathname!=='/'||origin.search||origin.hash||origin.username||origin.password||typeof input.token!=='string'||input.token.length<32||input.token.length>4096||!/^[A-Za-z0-9_-]{16,128}$/.test(input.space))throw Error('invalid private operator configuration');
 let response;try{response=await fetcher(new URL('/v1/operator/stats',origin),{redirect:'error',signal:AbortSignal.timeout(10000),headers:{Authorization:'Bearer '+input.token,'X-Hopsesh-Space':input.space}})}catch{throw Error('operator connection failed')}
 if(!response.ok)throw Error('operator status refused');
 const reader=response.body?.getReader();if(!reader)throw Error('invalid operator response');let size=0,chunks=[];
 for(;;){const{done,value}=await reader.read();if(done)break;size+=value.length;if(size>8192){await reader.cancel();throw Error('operator response exceeds limit')}chunks.push(value)}
 let data;try{data=JSON.parse(new TextDecoder().decode(Buffer.concat(chunks)))}catch{throw Error('invalid operator response')}
 const out={};for(const key of ['devices','messages','ciphertextBytes','pendingDeletions','tombstones','day','admittedFrames','admittedBytes','resetAt']){if(!Number.isSafeInteger(data[key])||data[key]<0)throw Error('invalid operator counters');out[key]=data[key]}
 if(typeof data.paused!=='boolean')throw Error('invalid operator pause state');out.paused=data.paused;
 out.limits={};for(const key of ['dailyFrames','dailyBytes']){if(!Number.isSafeInteger(data.limits?.[key])||data.limits[key]<1)throw Error('invalid operator limits');out.limits[key]=data.limits[key]}
 return out;
}
if(process.argv[1]===fileURLToPath(import.meta.url)){
 try{let text='';for await(const chunk of process.stdin){text+=chunk;if(Buffer.byteLength(text)>8192)throw Error('private operator input exceeds limit')};const out=await operatorStats(JSON.parse(text));process.stdout.write(JSON.stringify(out)+'\n')}
 catch{process.stderr.write('operator status failed; check private configuration, authentication and connectivity\n');process.exitCode=1}
}
