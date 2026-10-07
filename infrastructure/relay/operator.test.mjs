import test from 'node:test';
import assert from 'node:assert/strict';
import {operatorStats} from './operator.mjs';
const input={origin:'https://relay.hopsesh.codonic.dev',space:'approved-space-0001',token:'private-operator-credential-at-least-32'};
const counts={devices:2,messages:1,ciphertextBytes:500,pendingDeletions:0,tombstones:1,day:1,admittedFrames:1,admittedBytes:500,resetAt:172800,paused:false,limits:{dailyFrames:4096,dailyBytes:268435456}};
test('operator client fixes route, refuses redirects and emits only counts',async()=>{
 const out=await operatorStats(input,async(url,options)=>{assert.equal(url.href,input.origin+'/v1/operator/stats');assert.equal(options.redirect,'error');assert.equal(options.headers.Authorization,'Bearer '+input.token);return Response.json({...counts,email:'must not emit',token:'must not emit'})});
 assert.deepEqual(out,counts);
});
test('operator client refuses alternate origins, malformed counters and oversized responses',async()=>{
 for(const origin of ['http://relay.test','https://relay.test/query?secret=1','https://user:password@relay.test','https://relay.test/#secret'])await assert.rejects(operatorStats({...input,origin},()=>{throw Error('must not connect')}));
 await assert.rejects(operatorStats(input,async()=>Response.json({...counts,messages:'1'})));
 await assert.rejects(operatorStats(input,async()=>new Response('x'.repeat(9000))));
});
