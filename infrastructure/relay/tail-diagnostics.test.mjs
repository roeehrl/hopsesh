import test from 'node:test';
import assert from 'node:assert/strict';
import { sanitizeTail, tailDecoder } from './tail-diagnostics.mjs';

test('tail diagnostics drop arbitrary fields and retain only bounded correlation metadata', () => {
  const event = {
    outcome: 'exception', eventTimestamp: 1791608000000,
    event: { request: { url: 'https://secret-host/v1/messages?secret-query', headers: { Authorization: 'secret-token' }, body: 'secret-body' }, response: { status: 500 } },
    logs: [{ message: 'secret-console' }],
    exceptions: [{ name: 'Error', message: 'Durable Object reset because its code was updated.', stack: 'secret-stack at worker.mjs:123:45' }],
  };
  const result = sanitizeTail(event);
  assert.equal(result.route, '/v1/messages');
  assert.equal(result.status, 500);
  assert.equal(result.exceptions[0].category, 'code-update-reset');
  assert.deepEqual(result.exceptions[0].locations, ['worker.mjs:123:45']);
  assert.ok(!JSON.stringify(result).includes('secret'));
  event.outcome = 'secret-outcome'; event.event.request.url = 'https://private/secret-path';
  event.exceptions = Array(20).fill({ name: 'secret-name', message: 'secret-message', stack: 'secret-file.js:1:1' });
  const unknown = sanitizeTail(event);
  assert.equal(unknown.exceptions.length, 8);
  assert.equal(unknown.exceptions[0].category, 'unclassified');
  assert.ok(!JSON.stringify(unknown).includes('secret'));
});

test('tail parser handles split JSON, escaped braces, banners and oversized input', () => {
  const output = [];
  const decode = tailDecoder(event => output.push(event), 1024);
  const json = JSON.stringify({ outcome: 'exception', logs: ['secret } \\" {'], exceptions: [{ name: 'Error', message: 'Network connection lost.' }] }, null, 2);
  for (const character of 'Connecting...\n' + json) decode(character);
  decode(JSON.stringify({ logs: ['secret'.repeat(1000)] }));
  decode(JSON.stringify({ outcome: 'ok' }));
  assert.equal(output.length, 2);
  assert.equal(output[0].exceptions[0].category, 'network-lost');
  assert.equal(output[1].outcome, 'ok');
  assert.ok(!JSON.stringify(output).includes('secret'));
});
