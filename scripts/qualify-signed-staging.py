"""Execute the published, signature-verified staging binary on native Linux runners."""
import hashlib,json,os,pathlib,platform,subprocess,tarfile,tempfile
VERSION='0.5.0-staging.20261010.7011675'
SOURCE='701167583d0b147c5f2f265e763ff22e91310e9b'
ARCH={'x86_64':'amd64','aarch64':'arm64'}[platform.machine()]
ORIGIN=f'https://downloads.hopsesh.codonic.dev/releases/v{VERSION}/'
ROOT=pathlib.Path.cwd()
OUT=ROOT/'staging-evidence';OUT.mkdir()
archive=f'hopsesh_{VERSION}_linux_{ARCH}.tar.gz'
# Exercise the download client and restrictions used by Hopsesh's generated
# bootstrap, not Python urllib (rejected by this origin's Browser Integrity Check).
for name in ['checksums.txt','checksums.txt.sig',archive]:
 p=subprocess.run(['curl','--fail','--silent','--show-error','--proto','=https','--max-time','60','--max-filesize','33554432','--write-out','%{http_code}','--output',str(OUT/name),ORIGIN+name],capture_output=True,text=True,timeout=65)
 assert p.returncode==0 and p.stdout=='200',(name,p.returncode,p.stdout,p.stderr)
 assert (OUT/name).stat().st_size<=32*1024*1024
subprocess.run(['openssl','dgst','-sha256','-verify',str(ROOT/'packaging/release-key.pub'),'-signature',str(OUT/'checksums.txt.sig'),str(OUT/'checksums.txt')],check=True)
checks={}
for line in (OUT/'checksums.txt').read_text().splitlines():
 digest,name=line.split('  ',1);assert name not in checks;checks[name]=digest
assert hashlib.sha256((OUT/archive).read_bytes()).hexdigest()==checks[archive]
with tarfile.open(OUT/archive) as t:
 members=t.getmembers();assert [m.name for m in members]==['hopsesh','LICENSE','README.md','CHANGELOG.md']
 assert all(m.isfile() for m in members)
 binary=t.extractfile('hopsesh').read()
 for name in ['LICENSE','README.md','CHANGELOG.md']:
  assert t.extractfile(name).read()==subprocess.check_output(['git','show',f'{SOURCE}:{name}'])
 assert binary[:4]==b'\x7fELF'
 assert int.from_bytes(binary[18:20],'little')=={'amd64':62,'arm64':183}[ARCH]
exe=OUT/'hopsesh';exe.write_bytes(binary);exe.chmod(0o700)
report={'source':SOURCE,'version':VERSION,'architecture':platform.machine(),'nativeLinux':platform.system()=='Linux','archive':archive,'archiveSHA256':checks[archive],'binarySHA256':hashlib.sha256(binary).hexdigest(),'signatureVerified':True,'complete':False,'events':[]}
assert report['nativeLinux']
with tempfile.TemporaryDirectory(prefix='hopsesh-signed-qa-') as directory:
 home=pathlib.Path(directory);env=os.environ.copy()
 env.update(HOME=str(home),XDG_CONFIG_HOME=str(home/'config'),XDG_STATE_HOME=str(home/'state'),XDG_DATA_HOME=str(home/'data'),XDG_CACHE_HOME=str(home/'cache'),XDG_RUNTIME_DIR=str(home/'run'))
 for name in ['DISPLAY','WAYLAND_DISPLAY']:env.pop(name,None)
 (home/'run').mkdir(mode=0o700)
 config=home/'config/hopsesh/config.toml';config.parent.mkdir(parents=True)
 config.write_text('schema = 5\nrepos_dir = '+json.dumps(str(home/'repos'))+'\nlayout = "flat"\nupdate_check = "off"\n'+''.join(f'\n[agents.{a}]\ndisabled = true\n' for a in ['claude','codex','copilot','jules','devin','amp']))
 before=config.read_bytes()
 def run(*args,check=True):
  p=subprocess.run([str(exe),*args],env=env,text=True,capture_output=True,timeout=30)
  if check:assert p.returncode==0,(args,p.returncode,p.stdout,p.stderr)
  report['events'].append({'command':list(args),'exitCode':p.returncode,'stdout':p.stdout,'stderr':p.stderr})
  return p
 try:
  version=run('version').stdout;assert VERSION in version and 'commit 7011675' in version
  first=json.loads(run('runtime','start','--headless').stdout)
  repeat=json.loads(run('runtime','start','--headless').stdout)
  assert all(first[k]==repeat[k] for k in ['pid','epoch','namespace'])
  status=json.loads(run('runtime','status').stdout);assert status['mode']=='headless'
  assert status['epoch']==first['epoch']
  run('runtime','doctor')
  run('runtime','stop')
  assert run('runtime','status',check=False).returncode!=0
  assert config.read_bytes()==before
  report.update(complete=True,idempotentOwner=True,headless=True,cleanShutdown=True,configUnchanged=True)
 finally:
  run('runtime','stop',check=False)
  (OUT/'verified.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps({k:report[k] for k in ['source','architecture','complete','signatureVerified','archiveSHA256']}))
