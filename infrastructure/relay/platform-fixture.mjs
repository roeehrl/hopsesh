// Real workerd/SQLite/R2 integration server. Unlike `wrangler dev`, this has
// no hot-reload ProxyWorker between native clients and the platform runtime.
import {execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {Miniflare, Log, LogLevel, convertV4MiniflareOptions} from 'miniflare';
import {unstable_getMiniflareWorkerOptions} from 'wrangler';

const [root, portText, key, cert, ...overrides] = process.argv.slice(2);
const port = Number(portText);
if (!root || !key || !cert || !Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error('expected disposable root, port, TLS key and certificate');
}
const here = dirname(fileURLToPath(import.meta.url));
const config = JSON.parse(readFileSync(join(here, 'wrangler.jsonc'), 'utf8'));
config.main = resolve(here, config.main);
config.vars = {...config.vars, ENROLLMENT_ADMIN: 'fixture-admin-secret-with-32-bytes-minimum', RELAY_PAUSED: '0'};
for (const value of overrides) {
  const colon = value.indexOf(':');
  if (colon < 1) throw new Error('invalid fixture variable');
  config.vars[value.slice(0, colon)] = value.slice(colon + 1);
}
// Resolve config in the disposable directory: developer .dev.vars/.env files
// must never provide hosted credentials to local qualification.
const configPath = join(root, 'wrangler.json');
writeFileSync(configPath, JSON.stringify(config));
const output = join(root, 'build');
const bundle = join(output, 'worker.js');
const require = createRequire(import.meta.url);
execFileSync(process.execPath, [require.resolve('wrangler'), 'deploy', '--dry-run',
  '--config', configPath, '--outdir', output], {
  cwd: root, timeout: 15000, stdio: 'pipe',
  env: {...process.env, WRANGLER_SEND_METRICS: 'false'},
});
const {workerOptions, externalWorkers} = unstable_getMiniflareWorkerOptions(configPath);
// Wrangler has already resolved source module rules into this deployment bundle.
// Pass its explicit module instead of applying source globs a second time.
delete workerOptions.modulesRules;
const platform = new Miniflare(convertV4MiniflareOptions({
  host: '127.0.0.1', port, httpsKey: readFileSync(key, 'utf8'), httpsCert: readFileSync(cert, 'utf8'),
  cf: false, log: new Log(LogLevel.ERROR),
  unsafeLocalExplorer: true, unsafeObservability: true,
  resourcePersistencePath: join(root, 'platform-state'),
  isolatedResourcePersistencePath: join(root, 'platform-isolated-state'),
  workers: [{...workerOptions, name: config.name, modules: [{type: 'ESModule', path: bundle}]}, ...externalWorkers],
}));
let stopping;
const stop = () => stopping ??= platform.dispose().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
process.once('SIGINT', stop);
process.once('SIGTERM', stop);
await platform.ready;
