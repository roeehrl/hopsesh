// Opt-in, bounded staging diagnostics. Raw tail events never reach disk/stdout.
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';

const outcomes = new Set(['ok', 'exception', 'exceededCpu', 'exceededMemory', 'canceled', 'unknown']);
const routes = new Set(['/v1/messages', '/v1/notifications', '/v1/ack', '/v1/enrollment/register', '/v1/enrollment/renew', '/v1/enrollment/revoke']);
const messages = new Map([
  ['Durable Object reset because its code was updated.', 'code-update-reset'],
  ['Durable Object storage operation exceeded timeout which caused object to be reset.', 'storage-timeout-reset'],
  ['Durable Object is overloaded. Too many requests queued.', 'overloaded-requests'],
  ['Durable Object is overloaded. Too much data queued.', 'overloaded-data'],
  ['Durable Object is overloaded. Requests queued for too long.', 'overloaded-wait'],
  ['The script will never generate a response.', 'response-never-generated'],
  ['Network connection lost.', 'network-lost'],
]);

export function sanitizeTail(event) {
  const result = {
    recordedAt: new Date().toISOString(),
    outcome: outcomes.has(event.outcome) ? event.outcome : 'other',
    route: 'other',
    kind: event.event?.request ? 'http' : event.event?.webSocketEvent ? 'websocket' : event.event?.scheduledTime ? 'alarm' : 'other',
    exceptions: [],
  };
  if (Number.isSafeInteger(event.eventTimestamp) && event.eventTimestamp >= 0 && event.eventTimestamp < 8.64e15) {
    result.eventAt = new Date(event.eventTimestamp).toISOString();
  }
  try {
    const path = new URL(event.event.request.url).pathname;
    if (routes.has(path)) result.route = path;
  } catch { /* Neither URLs nor parser errors are emitted. */ }
  if (Number.isInteger(event.event?.response?.status) && event.event.response.status >= 100 && event.event.response.status <= 599) {
    result.status = event.event.response.status;
  }
  for (const exception of (Array.isArray(event.exceptions) ? event.exceptions : []).slice(0, 8)) {
    const message = typeof exception?.message === 'string' ? exception.message : '';
    const stack = typeof exception?.stack === 'string' ? exception.stack : '';
    result.exceptions.push({
      name: ['Error', 'TypeError', 'RangeError', 'ReferenceError', 'SyntaxError'].includes(exception?.name) ? exception.name : 'other',
      category: messages.get(message) ?? 'unclassified',
      messageHash: createHash('sha256').update(message).digest('hex'),
      locations: [...stack.matchAll(/\bworker\.mjs:\d{1,7}:\d{1,7}\b/g)].slice(0, 8).map(match => match[0]),
    });
  }
  return result;
}

// Wrangler prints pretty JSON, possibly split anywhere across stream chunks.
// Ignore banners and discard oversized events without retaining their contents.
export function tailDecoder(emit, maxBytes = 256 * 1024) {
  let depth = 0, quoted = false, escaped = false, buffer = '', size = 0;
  return chunk => {
    for (const char of chunk) {
      if (!depth) {
        if (char !== '{') continue;
        depth = 1; quoted = false; escaped = false; buffer = '{'; size = 1;
        continue;
      }
      size += Buffer.byteLength(char);
      if (size <= maxBytes) buffer += char;
      else buffer = '';
      if (quoted) {
        if (escaped) escaped = false;
        else if (char === '\\') escaped = true;
        else if (char === '"') quoted = false;
      } else if (char === '"') quoted = true;
      else if (char === '{') depth++;
      else if (char === '}') depth--;
      if (!depth && buffer) {
        try { emit(sanitizeTail(JSON.parse(buffer))); } catch { /* Drop invalid input. */ }
        buffer = '';
      }
    }
  };
}

if (import.meta.main) {
  const seconds = Number(process.argv[2]);
  if (!Number.isInteger(seconds) || seconds < 1 || seconds > 7200) {
    throw new Error('Supply a capture duration in seconds from 1 to 7200');
  }
  let count = 0;
  const child = spawn('./node_modules/.bin/wrangler', ['tail', 'hopsesh-relay-experimental', '--format', 'json'], {
    cwd: import.meta.dirname,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, WRANGLER_WRITE_LOGS: 'false', WRANGLER_SEND_METRICS: 'false', NO_COLOR: '1' },
  });
  // Suppress arbitrary stderr too; only the child exit status is reported.
  child.stderr.resume();
  let stopping = false, killTimer;
  const stop = () => {
    if (stopping) return;
    stopping = true;
    child.kill('SIGTERM');
    killTimer = setTimeout(() => child.kill('SIGKILL'), 5000);
  };
  const timer = setTimeout(stop, seconds * 1000);
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', tailDecoder(event => {
    if (count >= 4096) return stop();
    if (event.exceptions.length || event.outcome !== 'ok' || count === 0) {
      count++;
      process.stdout.write(JSON.stringify(event) + '\n');
    }
  }));
  child.on('error', () => { process.exitCode = 1; });
  child.on('close', (code, signal) => {
    clearTimeout(timer); clearTimeout(killTimer);
    process.stdout.write(JSON.stringify({ captureEnded: true, events: count, code, signal }) + '\n');
    if (!stopping && code !== 0) process.exitCode = 1;
  });
  process.on('SIGTERM', stop);
  process.on('SIGINT', stop);
}
