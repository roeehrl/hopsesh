import test from 'node:test';
import assert from 'node:assert/strict';
import { sanitizeTail, shouldCaptureTail, tailDecoder } from './tail-diagnostics.mjs';

test('hosted daily write quota is recognizable without exposing arbitrary exception text', () => {
  const message = 'Exceeded allowed rows written in Durable Objects free tier.';
  const event = sanitizeTail({ outcome: 'exception', event: { scheduledTime: 1791618033338 }, exceptions: [{ name: 'Error', message }] });
  assert.equal(event.kind, 'alarm');
  assert.equal(event.exceptions[0].category, 'daily-write-quota');
  // Matches all five errors retained from the October 10 hosted failure.
  assert.equal(event.exceptions[0].messageHash, '68ef9806a386bf8a1e43fe24b9c2362373e1641d085ac261f99ff3e62c4b7a2b');
  assert.equal(shouldCaptureTail(event), true);
  assert.ok(!JSON.stringify(event).includes(message));
  const changed = sanitizeTail({ exceptions: [{ message: message + ' private tenant details' }] });
  assert.equal(changed.exceptions[0].category, 'unclassified');
  assert.ok(!JSON.stringify(changed).includes('private tenant details'));
});

test('handled server and rate failures remain visible without recording successful traffic', () => {
  for (const status of [101, 200, 201, 204, 400, 403, 429, 500, 502, 503]) {
    const event = sanitizeTail({ outcome: 'ok', event: { response: { status }, request: { url: 'https://private/v1/messages?secret-query', body: 'secret-body' } } });
    assert.equal(shouldCaptureTail(event), status === 429 || status >= 500);
    assert.equal(shouldCaptureTail(event, true), true);
    assert.ok(!JSON.stringify(event).includes('secret'));
  }
  assert.equal(shouldCaptureTail(sanitizeTail({ outcome: 'exception' })), true);
  assert.equal(shouldCaptureTail(sanitizeTail({ outcome: 'ok', exceptions: [{ message: 'private-details' }] })), true);
});

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
