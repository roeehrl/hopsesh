# Machines

hopsesh reads your other machines over SSH: their sessions, and what their agents keep
on disk. That needs nothing installed there. Everything else a machine can do for you needs
hopsesh running on it, in your login session:

| On another machine | SSH only | hopsesh there, not running | hopsesh running in the login session |
| --- | --- | --- | --- |
| List its sessions | Yes | Yes | Yes, with live state |
| Who is signed in | Over SSH; on a Mac the Keychain is locked to SSH, so it is unknown | Same | Answered by hopsesh there, Keychain included, per tool |
| Bring a session here | Yes | Yes | Yes; wait for a working one, or join it there |
| Send a session there | No | Yes | Yes |
| Continue a session there | No | No | Yes: it runs there; you work in it here |
| Protect an original left there | No, and the app says so | When its hooks are set up there | Set up from here |
| Hooks and skill | No | By hand there | Set up from here |
| Add an account there | No | No | Yes, signing in from this machine's browser |

## Setting hopsesh up on a machine

Machines › **Set up hopsesh on ‹machine›…**, or `hopsesh hosts setup <machine>`, only when you
ask (scans never install anything):

1. Copies this hopsesh to `~/.hopsesh/bin` there over SSH: this very program when it runs
   there too (a macOS program is universal), else the same release for that machine,
   verified like an update (`checksums.txt` and its signature). A hopsesh you installed there
   yourself (the app, a package manager, a link in `~/.local/bin`) is left as it is.
2. Turns on receiving sessions there.
3. Registers `hopsesh serve` to start in your login session there at each login: a
   LaunchAgent on macOS, a systemd user service on Linux, a task at sign-in on Windows. No
   administrator rights. `hopsesh serve uninstall` there turns it off.
4. The identity this machine kept for it while it had no hopsesh becomes its own, so the
   history recorded since stays that machine's.

On a Mac nobody is logged in to, hopsesh serve starts at the next login; until then the
machine works over SSH only.

## hopsesh serve

`hopsesh serve` listens on a private socket (`~/.hopsesh/run/serve.sock`, in a folder only you
can open, and only for your own processes). Other machines still connect with SSH and run
`hopsesh peer`; that hands the conversation to hopsesh serve, which answers from where your
sign-ins and apps are. It opens no network port.

It also owns the terminals of sessions it runs for another machine, so they outlive the
connection that shows them: `hopsesh terminals run <machine>` starts one (a session there, a
new session, or an account's sign-in) and `hopsesh terminals attach <machine> <id>` shows it
again. In the app these are tabs that say which machine they run on. A session that is open
on that machine in another program is never run a second time.

## Versions

Two machines agree on the newest peer protocol both speak (protocol 6 now, down to 5, which
hopsesh 0.4 speaks). A machine on 0.4 can still send and receive sessions; the Machines screen
offers **Update hopsesh on ‹machine›**, which is the same setup.

## What is read, and what never is

hopsesh reads who is signed in from each tool's own public answers and identity fields: Claude
Code's `auth status` and the account ids in `~/.claude.json`, Claude desktop's last known
account id and its session index folders, Codex's `account/read` and the account and user ids
in `auth.json`. It never reads a token: not the Keychain's, `.credentials.json`, Codex's tokens,
or Claude desktop's token caches and cookies. Machines exchange fingerprints and emails only;
no credential ever crosses machines.

## Checking every machine

`hopsesh doctor --all-machines` checks each allowed machine: SSH, hopsesh there and how it runs,
its agents, its protection hooks and its skill. Settings › **Machines** shows the same for each
machine, sets up what it can from here (every agent folder's hooks and skill in one step), and
holds this machine's command, skill and protection hooks in full.
