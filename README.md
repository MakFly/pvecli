# pvecli — a Proxmox VE CLI, built to learn the API

`pvecli` is a remote command-line client for **Proxmox VE**, written in Go. It
drives a homelab node through the `/api2/json` REST API — inventory, VM and LXC
lifecycle, tasks, storage, ACLs, backups — and bridges that API to a
Terraform / Ansible pipeline.

It is **not** a clone of `pvesh`. `pvesh` only exists *on* the node, behind SSH.
`pvecli` is a remote, typed, scriptable client with the guardrails the web UI
does not offer: `--dry-run` everywhere, JSON output, real task polling, generated
Ansible inventories.

> **Status: M0 → M10 and M12 are closed. M11 and M13 are open on a proof, not
> on code.** `pvecli` reads a
> node's whole inventory, creates, configures, clones, snapshots, starts and
> destroys both virtual machines and LXC containers, explains a `403` instead of
> suggesting you escalate, has been used to destroy a running VM and bring its
> service back, drives a full Terraform → inventory → Ansible chain whose drift
> it measures, reads the network configuration and applies it, groups resources
> into pools, feeds storages by URL or by upload, and moves a guest between
> nodes — over verified TLS, with a non-root token. It ships as a static binary
> for macOS and Linux, completes VMIDs at the `Tab` key, and runs from the node
> itself.
>
> Since M9 it also **declares** a VM and the services that go in it — one
> command, no HCL to write — and ends the run by saying how to get in. Since M10
> it drives Cloudflare Tunnel, so a service reaches the web without a single
> port opened on the router. Since M12 it mints its **own** first token from a
> password, so the bootstrap no longer needs SSH, and finds the token secret in
> one of three sources instead of one. M13 reaches **inside** a guest without
> SSH — the QEMU agent for a VM, the console for a container, which has no exec
> endpoint at all — filters guests through the PVE firewall, manages scheduled
> backup jobs, and mints the **custom role** those jobs need, so granting one
> privilege no longer means handing over the whole node. Since then it also
> declares storage backends, carries a `caddy` role in its catalogue, and
> reboots the node itself — proving it came back by an uptime that *fell*.

```sh
pvecli iac scaffold
pvecli vm declare app-01 --vmid 220 --cores 2 --memory 8192 \
    --ip 192.168.1.220/24 --gateway 192.168.1.1 \
    --with docker,postgresql
pvecli iac plan && pvecli iac apply
pvecli iac configure --playbook pvecli.yml --idempotence
```

```
accès aux services installés :
HÔTE    ACCÈS                 VALEUR
app-01  ip                    192.168.1.220
app-01  ssh                   ssh ops@192.168.1.220
app-01  docker                29.7.1
app-01  postgresql.host       192.168.1.220:5432
app-01  postgresql.database   app
app-01  postgresql.user       app
app-01  postgresql.password   → trousseau : security find-generic-password …
```

Growing it later is one flag, because a declared VM is **data**, not code:

```sh
pvecli vm declare app-01 --memory 16384 --disk 25 && pvecli iac apply
```

`--with` reaches into a service catalogue (`internal/catalog/assets/catalog.yaml`,
plus one Ansible role per entry), and one of those services is `caddy`: a
**shared** reverse proxy, not a per-project one. Its Caddyfile is generated and
deliberately route-free — each project drops its own fragment in
`/etc/caddy/conf.d/`, written by its own deploy, and Caddy imports whatever is
there. That fragment is validated *before* it is reloaded, and the proxy is
reloaded rather than restarted: one project shipping a bad route must not drop
the connections of every other project sharing the same Caddy. It publishes no
port in the catalogue, on purpose — Cloudflare terminates the public TLS, and
this Caddy only ever sees loopback traffic from `cloudflared`.

## Why this exists

This is a learning project with a product's discipline. The goal is to
understand Proxmox VE by building the tool that talks to it, endpoint by
endpoint, rather than by clicking through the web interface.

Two rules make it work:

1. **No endpoint written from memory.** Every path is checked against the
   official PVE 9.x API viewer before it is implemented, and recorded with its
   source in [`docs/API-MAP.md`](docs/API-MAP.md).
2. **No milestone without proof.** Each batch of stories closes on a command
   that must actually run against a real node — not on a passing test suite.

Every lesson learned, including the mistakes, goes into
[`docs/LEARNING-LOG.md`](docs/LEARNING-LOG.md).

## The write contract

Every mutation goes through one pipeline. A write that does not is a bug, not a
variant:

```
1. PRE-READ    does the target exist? is it locked?
2. PLAN        the RESOLVED payload on stderr — not a paraphrase
3. GATE        --dry-run stops here; otherwise confirm
               (destructive: retype the target id, not "y")
4. WRITE
5. POLL        HTTP 200 is an acceptance, not a success. Wait for exitstatus.
6. LOG         on failure: last 20 lines of the task log, exit code 4
7. POST-READ   independent proof — and it is THIS that gets printed
```

Steps 5 and 6 are skipped for a synchronous mutation. Step 7 never is: a test
fails if it is.

## Design principles

- **`HTTP 200` is not success.** Proxmox mutations return a UPID — a task id. A
  command that does not poll the task to its `exitstatus` and then re-read the
  resource is considered non-compliant.
- **Verified TLS, not `--insecure`.** Self-signed lab certificates are handled
  by SHA-256 fingerprint pinning. `--insecure` exists, works, and complains
  loudly on stderr every single time.
- **Least privilege.** A dedicated, expiring API token with `privsep=1` and the
  narrowest role that does the job. `root@pam` is never used.
- **Secrets never touch disk.** The token secret lives in the OS keychain and
  reaches the process through the environment. It is redacted from every trace,
  and a test scans the whole `--verbose` output to prove it.
- **stdout is data.** Progress, warnings and prompts go to stderr, so
  `pvecli vm ls -o json | jq` always works.
- **`cmd/` never speaks HTTP.** Commands call services, services call the API
  client, everything is behind interfaces — the test suite runs with no Proxmox
  node powered on.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/MakFly/pvecli/main/install.sh | sh
```

It detects the platform, resolves the latest release, **verifies the SHA-256
before installing**, moves the binary into `~/.local/bin` and installs the
[AI agent](#ai-agent). A checksum that does not match aborts the install and
leaves nothing behind — an installer piped into a shell runs code that arrived
over the network, and the least it owes you is proof that the byte on your disk
is the byte that was published.

| Variable | Effect |
| --- | --- |
| `PVECLI_VERSION` | pin a version instead of the latest |
| `PREFIX` | install root (default `~/.local` → `~/.local/bin`) |
| `PVECLI_NO_AGENT=1` | skip the Claude Code agent |
| `PVECLI_ONLY_IF_NEWER=1` | do nothing if that version is already installed |

### Staying up to date

The shortest path is the binary itself:

```sh
pvecli upgrade             # ou : pvecli --upgrade
pvecli upgrade --dry-run   # dit ce qu'il téléchargerait et où, sans rien écrire
pvecli upgrade --force     # depuis un build « dev », ou pour réinstaller à version égale
```

It applies the same rule as `install.sh`: the published `SHA256SUMS` is checked
against the bytes received **before** anything touches the disk, and the
replacement is a rename within the destination directory — an interrupted
download leaves the working binary in place, never a half-written one.

It refuses two things by default, both liftable with `--force`: overwriting a
locally built (`dev`) binary, which almost always contains *more* than the
latest release, and reinstalling a version already on disk.

Three ways to keep a fleet current, from most to least automatic. The timer
below installs silently; `pvecli upgrade` installs when *you* ask;
`update check` only ever tells you.

`PVECLI_ONLY_IF_NEWER=1` turns the installer into a cheap no-op when the binary
on disk is already the published one — which is what makes it safe to run on a
schedule. The schedule itself is two systemd user units:

```sh
scripts/autoupdate/install-timer.sh              # once a day, from now on
scripts/autoupdate/install-timer.sh --uninstall  # and back out
```

`systemd --user`, not root: pvecli installs into `~/.local/bin`, and there is
nothing here that needs privilege. `Persistent=true`, so an evening spent
powered off doesn't silently skip a day.

It runs the **local copy** of `install.sh` and not `curl … | sh`, deliberately.
A daily pipe from the network grants whoever takes the repository one code
execution per day on your machine; here the script is versioned and reviewed,
updating it stays a deliberate `git pull`, and the bytes it installs are still
proven against the release's `SHA256SUMS`. Reviewed script, proven bytes.

```sh
systemctl --user list-timers pvecli-update.timer   # when it next fires
systemctl --user start pvecli-update.service       # check right now
journalctl --user -u pvecli-update.service -n 20   # what it did
```

### Getting notified, without auto-installing

The timer above *installs* silently. If you'd rather just be *told* a new
release exists — on your own schedule, without a background service touching
your disk — `pvecli update check` covers that instead, and the two coexist:

```sh
pvecli update check           # à jour / vX → vY disponible / dev / vérification impossible
pvecli update check --force   # ignore le cache de 24h, refait l'appel réseau
```

For a heads-up at every new terminal:

```sh
pvecli update install-hook             # wires it into ~/.zshrc / ~/.bashrc
pvecli update install-hook --uninstall # removes it again
pvecli update install-hook --print     # just prints the snippet, writes nothing
```

`install.sh` and `make install` already call this after installing the
binary — a failure there does not fail the install, it only means the
notification stays unwired. `PVECLI_NO_SHELL_HOOK=1` skips it outright.

The snippet makes two separate calls, on purpose — one command cannot both
answer the prompt instantly and be allowed to wait on the network:

- `pvecli update check --notify` runs in the **foreground**: it only ever
  reads the 24h cache (`$XDG_CACHE_HOME/pvecli/update-check.json`), never the
  network, so it can never block the prompt. Silent unless an update is
  already known, and silent for a locally-built (`dev`) binary.
- `pvecli update check --refresh` runs **detached in the background**: it is
  the one allowed to reach GitHub (2s timeout), and it never prints anything,
  success or failure — it only updates the cache for the *next* terminal.

The trade-off is deliberate: the notification is always one terminal behind
the truth. See `cmd/assets/update-notify.sh` for why the backgrounding
itself is written the way it is (avoiding zsh's own job-control noise).

Only `linux/amd64` and `darwin/arm64` are published. Anywhere else, build from
source — Go 1.26+:

```sh
git clone https://github.com/MakFly/pvecli.git
cd pvecli
make build          # → ./pvecli, version and commit injected at link time
make install        # → ~/.local/bin/pvecli, AND the agent in ~/.claude/agents/
```

`make install` deliberately does two things. The binary lands in `PREFIX`
(`~/.local` by default — `/usr/local` needs `sudo`, and an install target that
asks for root to place a user binary is a target people run under `sudo` without
thinking). The second is the Proxmox subagent.

Onto the node itself:

```sh
make release VERSION=v0.1.0          # dist/… + SHA256SUMS
make install-node VERSION=v0.1.0     # scp, then `pvecli --version` there
```

`install-node` copies to a `.new` path and moves it into place, so a binary
being replaced while it runs is not a half-written file. The node it targets is
`NODE=192.0.2.23` by default; override it. The node never receives the agent:
a hypervisor does not run Claude Code.

### Releasing

The CD half is driven from the Actions tab — **Release → Run workflow** — with
one choice:

| Ampleur | Bumps | `v1.4.9` becomes |
| --- | --- | --- |
| `low` | patch — a fix | `v1.4.10` |
| `mid` | minor — a compatible addition | `v1.5.0` |
| `high` | major — a break | `v2.0.0` |

The workflow reads the latest tag, computes the next version, creates and pushes
the tag, then publishes. `dry_run` builds and verifies everything without
tagging or publishing. Pushing a `vX.Y.Z` tag by hand takes the same path,
minus the computation.

Nothing is published that has not been proved, in this order:

1. `verify` **reuses `ci.yml`** rather than copying its steps — two lists that
   must stay identical always drift, and it is the copy that silently loses a
   check;
2. each binary is **executed on its own platform** (an Ubuntu runner and a macOS
   runner), not merely compiled. `-ldflags` that fail to apply produce a
   perfectly valid binary that answers `dev`, and nothing flags it at build
   time;
3. the checksums are re-confronted with their files.

Build provenance used to be attested here as a fourth step. GitHub has since
restricted that API to public repositories and to organisation-owned private
ones, and this repository is neither, so the step was removed rather than left
to fail every release. What remains is a SHA-256 per binary: it proves a file
has not been altered, and says nothing about where it came from. If this
repository ever goes public, `actions/attest-build-provenance` is worth putting
back.

### First run

You need an API token. `root@pam` works and is exactly what this tool is built
to avoid — create a dedicated one instead. `pvecli login` does it for you: it
trades a password for a ticket (`POST /access/ticket`, the same call the web UI
makes), and uses that ticket to create the user, the token and its ACL.

```sh
pvecli config init --endpoint https://pve.example:8006 --node pve
pvecli config trust     # pin the certificate — stronger than --insecure, costs one command
pvecli login --user root@pam
# → the token secret is printed ONCE. PVE does not store it in clear and will
#   never show it again.
```

The password is only ever typed at the prompt, without echo (`PVE_PASSWORD` in a
script). `login` is replayable: an existing user and ACL are reapplied quietly,
an existing token is left alone — and cannot hand back its secret a second time.

<details>
<summary>Without a PVE password — the same thing by hand, on the node</summary>

```sh
pveum user add automation@pve
pveum acl modify / --roles PVEAuditor --users automation@pve
pveum user token add automation@pve pvecli --privsep 1
# → note the secret. It is shown ONCE and never again.
```

</details>

The secret never goes in a file and never goes in a flag — `ps` and the shell
history both read flags, which is why there is no `--token-secret`. Three
sources are tried, first one to answer wins. Pick **one** — these are
alternatives, not steps:

```sh
export PVE_API_TOKEN_SECRET='<the secret>'          # 1. environment
pvecli config set secret_command 'pass show pve/token'  # 2. a command whose stdout IS the secret
pvecli auth set-secret                              # 3. the OS keyring, prompted without echo
pvecli auth status                                  # which source answered, never the value
```

`auth status` says **ABSENT** when the secret is not *reachable* — not when it
does not exist. It cannot know the second question. If it says that while you
are sure the secret is on disk somewhere, the missing piece is usually the
wiring, not the secret: point source 2 at the file.

Then:

```sh
pvecli doctor           # network → TLS → auth → node → privileges, in that order
pvecli vm ls            # the first real answer
```

`doctor` is the command to run when anything is wrong. It walks the chain in
order and stops at the first broken link, so the answer is which layer failed,
not that "it does not work".

### Shell completion

```sh
pvecli completion zsh > "${fpath[1]}/_pvecli" && exec zsh
pvecli vm show <Tab>        # → the existing VMIDs, with their names
```

The completion is dynamic: it reads `GET /cluster/resources` to offer VMIDs,
nodes, storages, pools and tags. The answer is cached for ten seconds so
hammering `Tab` does not hammer the API, and if the node is unreachable it says
nothing at all rather than printing an error into the prompt.
`pvecli completion --help` covers bash, fish and powershell.

### AI agent

`make install` also writes a Claude Code subagent into the user's **global**
configuration:

```sh
pvecli ai install          # → ~/.claude/agents/proxmox-ops.md
pvecli ai status           # absent | à jour | diffère
pvecli ai print            # the definition, to stdout, writing nothing
```

The definition is `go:embed`-ed into the binary, so it travels with the CLI it
describes and `ai install` fetches nothing. An agent documenting flags the local
binary does not have is worse than no agent at all.

What it carries is what `--help` cannot: *an acceptance is not a result*, the
`managed` ownership guard, the deliberate refusals (`Sys.Modify`,
`Permissions.Modify` — reported, never worked around), the reserved 900-999
range, the destructive-action protocol, and the traps that actually cost time
here — `8192` and not `8`, the missing guest agent that turns an 18-second apply
into twelve minutes, Debian's default vhost answering `200 OK` with its own
page.

```
> crée-moi une VM 4 vCPU 16 Go nommée api-01
  ▸ proxmox-ops: doctor → main.tf → iac plan → iac apply → iac configure
```

Install refuses to overwrite a file that differs from the embedded one: the
difference is either a customisation or an older version, and both are worth
reading before losing. `--force` settles it. `make uninstall` removes the binary
and leaves the agent, for the same reason.

The node itself never receives the agent — `make install-node` is a different
target, and a hypervisor does not run Claude Code.

## Usage

```sh
pvecli --version    # version of this binary
pvecli version      # version of the Proxmox node (GET /version)

pvecli config init --endpoint https://pve.example:8006 --node pve
pvecli config trust                     # pin the node's certificate fingerprint
pvecli config show                      # effective config, and where each value came from
pvecli doctor                           # network → TLS → auth → node → privileges

pvecli node ls
pvecli vm ls -o json | jq '.[].name'
pvecli vm show 211
pvecli storage content local --content iso
pvecli task ls --running
```

Creating a virtual machine, guardrails included:

```sh
# See the resolved payload. Nothing is sent.
pvecli vm create 211 --name lab-app-01 --cores 2 --memory 2048 \
    --import-from 'local:import/debian-13-genericcloud-amd64.qcow2' \
    --cloud-init --ci-user debian --ssh-keys ~/.ssh/id_ed25519.pub \
    --ip dhcp --dry-run

# Same command without --dry-run: confirms, writes, follows the task to its
# exitstatus, then re-reads the guest and prints THAT.
pvecli vm start 211
pvecli vm shutdown 211
```

Those first two lines are not redundant. `--version` answers offline, with no
token and no network. `version` needs the network, a verified TLS chain and a
valid token. Two commands that fail for entirely different reasons should not
share a name — most CLIs get this wrong.

Containers, unprivileged unless you insist otherwise:

```sh
pvecli storage content local --content vztmpl
pvecli lxc create 120 --hostname web \
    --ostemplate local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst \
    --rootfs local-lvm:8 --net vmbr0 --ip 192.0.2.120/24 \
    --ssh-keys ~/.ssh/id_ed25519.pub --dry-run
```

`unprivileged=1` is in that payload without being asked for. Root inside the
container is uid 100000 on the host; in a privileged one it is root on the host,
and the container boundary becomes the only thing between the two.

The root password is never a command-line argument — `ps` and the shell history
both read those. It comes from `--password-stdin` or `PVECLI_CT_PASSWORD`.

Destruction answers to whoever owns the resource:

```sh
$ pvecli vm rm 212
Error: le guest 212 porte le tag « managed » : il appartient à Terraform, pas à toi.
  détruis-le par son propriétaire :  terraform destroy -target=…
  sinon le state décrira une ressource qui n'existe plus.
```

That check runs in the pre-read, before a single write leaves the process.
`--force-unmanaged` lifts it, and says in the same breath that it should not be
used: an operator who cannot override a guard works around the tool instead.

A backup is only worth what a restoration proves:

```sh
pvecli backup run 212 --storage local --mode snapshot --compress zstd
pvecli backup ls --check      # ← the guests with NO backup. RPO: infinite
pvecli backup restore local:backup/vzdump-qemu-212-….vma.zst --newid 910
```

`--check` is the useful half: a listing shows what exists, and the thing worth
knowing is what does not. `run` never prunes unless asked — the API's own
default deletes older archives — and its proof is the new archive appearing on
the storage, not the task's `exitstatus`. `restore` never overwrites a live
guest, because restoring over the original destroys the thing the backup was
meant to protect before anyone has checked the archive is any good.

But an on-demand backup only proves someone was there to launch it. The backup
that will actually exist on the day of the failure is the *scheduled* one — and
it is also the only one whose failure is silent:

```sh
pvecli backup job ls                       # ← empty means: RPO infinite, cluster-wide
pvecli backup job create --vmid 220,221 --storage pbs-infra \
    --schedule '02:30' --keep-last 3 --keep-daily 7
pvecli backup job set  vzdump-abc --enabled=false   # suspend, don't delete
pvecli backup job rm   vzdump-abc                   # retype the id to confirm
```

Three guards that are not in the API. `create` **requires** a retention: the
schema's default is `keep-all=1`, so a job left alone writes forever and fills
the storage — causing the very disk failure the backup existed to absorb. `ls`
shows the **next run**, because a schedule the node did not parse looks exactly
like a healthy job in a list of names. And `set` **merges** the retention
instead of replacing it: `prune-backups` is a single value API-side, so sending
only the counter you just changed would erase the others — and the next run
would delete archives nobody asked to delete.

The listing also reads `remove`, the switch that arms the retention. A policy
written but disarmed shows up as `keep-last=3 (INERTE : remove=0)` rather than
as a policy, because the reassuring version of that line is the dangerous one.

Those scheduled jobs need `Sys.Modify` — and among the node's built-in roles,
only `Administrator` carries it. Since an ACL grants a *role* and never a
privilege, the least-privilege way out is a custom role:

```sh
pvecli access role add ops-backup --privs Sys.Audit,Sys.Modify
pvecli access acl set --path / --role ops-backup --token automation@pve!pvectl
pvecli access role set ops-backup --add-priv Datastore.Allocate   # union, computed here
pvecli access role rm  ops-backup                 # lists who loses the rights first
```

`--add-priv`/`--rm-priv` read the role, merge locally and send the **full**
list: the API's `append` would compute the union node-side, so `--dry-run` could
not show the result — and removing a privilege has no API primitive at all.
Losing a privilege is treated as destructive, because every identity holding the
role loses it without any ACL being re-read.

The name cannot start with `PVE`: that namespace is reserved, and the refusal
comes from the node itself — `create_role` rejects `/^PVE/i`, **case
insensitively**. So a custom role can *not* be called `PVEBackupJobAdmin`; call
it `ops-backup`. `Administrator` and `NoAccess` are taken too.

A `403` is an information, not an obstacle:

```sh
$ pvecli lxc start 120                                    # → HTTP 403, exit 3
$ pvecli access whoami --can VM.PowerMgmt --path /vms/120  # → non, exit 1
$ pvecli access acl set --path /vms/120 --role PVEVMAdmin --token …
$ pvecli lxc start 120                                    # → running
```

Four commands, one identity throughout. The fix is a targeted ACL on the one
guest concerned — never a switch to `root@pam`, never `Administrator` on `/`
(which `acl set` refuses outright unless you spell out `--i-know-what-im-doing`).
`whoami --can` answers on stdout and in the exit code, so a script can branch on
it.

The network is the one place where the tool deliberately stops short:

```sh
pvecli net ls pve            # the ATTENTE column marks what a pending change touches
pvecli net apply pve         # retype the node name — this is what can cut the node off
pvecli net revert pve        # the reflex to know BEFORE you need it
```

PVE separates *writing* the network configuration from *applying* it, and that
gap is the whole safety net: until `apply`, nothing has moved and `revert`
throws the draft away. `pvecli` reads, applies and reverts — it does not create
or edit interfaces, because a form that validates what you type is a better
place for that.

The pending diff does not arrive in the API's `data` envelope; it comes back as
a sibling key that a client unwrapping `data` never sees. That is precisely the
thing an operator must see before touching anything.

Storages are fed by the node, not through your laptop:

```sh
pvecli storage download-url local --content iso \
    --url https://…/alpine-virt-3.21.4-x86_64.iso \
    --checksum c72ea5… --checksum-algorithm sha256
pvecli storage upload local ./image.qcow2 --content import   # local file, multipart
pvecli storage rm local local:iso/alpine-virt-3.21.4-x86_64.iso
```

`download-url` opens the connection **from the node**: a 4 GB image travels over
the node's uplink, not yours. The checksum is not decoration — the image you
drop today becomes the template you clone tomorrow, and an alteration in transit
propagates to every clone without ever announcing itself. Omit it and the
command says so; get it wrong and the node deletes what it downloaded.

Declaring **where** the cluster may write is a different family — `storage def`,
in a sub-name, because `storage rm <storage> <volid>` already deletes a
*volume*: a one-argument `storage rm <storage>` would silently delete the whole
storage definition when you forgot the volid.

```sh
pvecli storage def ls                       # warns when no backup target lives off-node
pvecli storage def add nas-backup --type nfs \
    --server 192.168.1.50 --export /export/pve --content backup
export PVE_STORAGE_PASSWORD="…"             # cifs and pbs only
pvecli storage def add pbs-infra --type pbs --server pbs.lan \
    --datastore archives --username archiver@pbs --content backup
pvecli storage def set nas-backup --disable # suspend, reversible
pvecli storage def rm nas-backup            # removes the CONFIG ENTRY, not the data
```

The password never travels as a flag: it comes from `PVE_STORAGE_PASSWORD` or a
masked prompt, because a flag is visible in `ps` to every user of the machine
and stays in the shell history. `rm` deletes the entry in
`/etc/pve/storage.cfg` — the archives on the share stay where they are — but it
first names every scheduled backup job writing there, since such a job then
fails on every run, silently. These writes need `Datastore.Allocate` on
`/storage`, not `Sys.Modify`: the built-in `PVEDatastoreAdmin` role already
carries it.

Rebooting the **node** — the widest blast radius in the CLI, so it asks you to
retype the node's name:

```sh
pvecli node reboot pve            # stops ALL guests; only onboot=1 ones come back
pvecli node reboot pve --wait 15m # how long the node gets to return (default 10m)
pvecli node reboot pve --no-wait  # returns immediately, and proves nothing
```

This is not `vm reboot`. The API returns **no UPID** here — a node cannot report
on a task whose whole point is that it stops answering — so the HTTP 200 is an
acceptance, not a success. The proof is taken from outside, and "the node
answers again" is not it: the node keeps answering for several seconds after
accepting the command, while systemd walks down its units. What `pvecli` waits
for is an **uptime that falls**, since uptime rises monotonically and can only
drop across a boot. Privilege is `Sys.PowerMgmt` on `/nodes/{node}`, not
`Sys.Modify` — a token that can rewrite the node's APT sources still cannot
power-cycle it.

The **"no valid subscription" dialog** the web UI raises on every login:

```sh
pvecli node nag status            # reads the node, changes nothing
pvecli node nag off               # suppresses it, then restarts pveproxy
pvecli node nag off --dry-run     # prints the exact script that would be sent
pvecli node nag on                # puts it back
```

This is the only command in the CLI that does **not** speak to `/api2/json`, and
that is forced, not sloppy: the dialog comes from
`/usr/share/javascript/proxmox-widget-toolkit/proxmoxlib.js`, a file on the
node's disk that no REST endpoint exposes whatever the token carries. So it goes
over your own `ssh` — your `~/.ssh/config`, your agent, your `known_hosts` —
with `BatchMode`, so a node that will not take the key fails immediately instead
of prompting for a password inside a pipeline.

The patch is one textual insertion carrying a marker,
`checked_command: function (orig_cmd) { orig_cmd(); return; /* pvecli:nag-off */`.
`orig_cmd()` is still called, so the command the user actually clicked still
runs. Detection looks for that marker and never for a count: the recipe that
circulates, `grep -c "orig_cmd();"`, returns **2 on a pristine file** — those are
the function's own legitimate calls — and therefore reports "already patched"
about a node that is not.

No `.bak` is left and no APT hook is installed, deliberately. A backup restored
after `apt upgrade` would reinstate a stale widget toolkit, and an APT hook is a
script that silently rewrites package-owned files forever. `nag on` is the exact
inverse of `nag off`, and `apt --reinstall install proxmox-widget-toolkit` is the
escape hatch in every case. The accepted cost: upgrading that package brings the
dialog back, `nag status` says so, `nag off` replays in a second.

Worth stating plainly: this circumvents a licence check. On a homelab it is
inconsequential. In production the Community subscription is the clean path, and
it is what opens the better-tested `pve-enterprise` repository.

## Configuration

Layered, in decreasing priority: **flags → environment → config file →
defaults**.

```yaml
# ~/.config/pvecli/config.yaml
current_context: lab
contexts:
  lab:
    endpoint: https://proxmox.example:8006
    token_id: automation@pve!pvectl
    node: pve
    tls:
      fingerprint: "9F:3D:1A:55:..."
    secret_command: pass show pve/token   # optional — its stdout IS the secret
    secret_source: command                # optional — pin the lookup to one source
```

Environment variables — `PVE_API_URL`, `PVE_API_TOKEN_ID`,
`PVE_API_TOKEN_SECRET`, `PVE_INSECURE` — are named to stay interoperable with
the `pve-api` bash client from the reference lab.

**`token_secret` is rejected inside the config file**, with an error naming the
line to delete and pointing at the three sources instead. A config file is
something you eventually commit. `secret_command` is the way to keep a secret
that lives in a file usable without exporting anything: what is written down is
the *command*, never the value. `secret_source` pins the lookup to a single
source, so a mistake shows up as an error instead of being silently caught by a
staler source.

`pvecli config show` prints the *effective* configuration together with the
layer each value won on, so the precedence is observable rather than assumed:

```
contexte      lab                        (fichier)
endpoint      https://autre:8006         (env PVE_API_URL)
node          pve                        (fichier)
token_secret  <défini>                   (env PVE_API_TOKEN_SECRET)
```

### Reaching a node published behind Cloudflare Access

A tunnel can publish the Proxmox interface itself, not just a service in a
guest — the origin then needs `--no-tls-verify`, because PVE serves its API
under a self-signed certificate:

```sh
pvecli cf route add pve.example.com --tunnel lab-pve \
    --service https://192.168.1.23:8006 --no-tls-verify
```

Put a Cloudflare Access application in front of that hostname and the browser
gets a login screen. A CLI does not: Access turns away anything that is not a
browser, so `pvecli` presents a **service token**, from the environment only:

```sh
export PVE_API_URL="https://pve.example.com"
export CF_ACCESS_CLIENT_ID="…"
export CF_ACCESS_CLIENT_SECRET="…"
```

Both or neither — half a service token is refused before a socket is opened,
because Cloudflare would answer it with a 403 indistinguishable from a Proxmox
permission error. Through the tunnel the certificate is Cloudflare's, publicly
valid: **no `tls.fingerprint`, and no `--insecure`**. The pinned fingerprint of
the node is only meaningful on the LAN.

## Development

```sh
make build         # compile with -ldflags version injection
make test          # unit tests — no node needed
make lint          # go vet + golangci-lint
make cover         # coverage, FAILING under 70 % on internal/pve and internal/service
make fmt           # gofmt
make integration   # tests against a REAL node, VMIDs 900-999 only
make release       # static binaries + SHA256SUMS
make help          # every target
```

CI runs `make lint test cover` and nothing else: what breaks on the runner has
to reproduce locally with the same command. It never touches a node — the
integration tests sit behind a `//go:build integration` tag and are launched by
hand. CI still type-checks them, because a test nobody can compile is a test
nobody will run and it will never say so.

Two of the tests are project guardrails rather than unit tests, and neither is
skippable: one scans the whole `--verbose` output for the token secret, the
other fails on any endpoint the client declares and `docs/API-MAP.md` does not
document.

Exit codes: `0` success · `1` generic · `2` usage · `3` auth/authz ·
`4` PVE task failed · `5` confirmation refused.

## Roadmap

| Milestone | Scope | Proof that closes it | State |
| --- | --- | --- | --- |
| **M0** Foundation | Cobra skeleton, layered config, token auth, TLS pinning, error triage | `pvecli version` returns the node's real version, TLS verified, non-root token | ✅ 9/9 |
| **M1** Read | Full read-only inventory, renderers, test harness | `pvecli vm ls -o json \| jq` works | ✅ 8/8 |
| **M2** Tasks & state | UPID parsing, polling, write guardrails, start/stop | A `stop` shows the UPID, waits for `exitstatus`, re-reads state | ✅ 6/6 — closed by the container M3 produced: `lxc stop 120` shows the UPID, waits, re-reads `stopped` |
| **M3** Lifecycle | create / clone / set / snapshot / template / rm, VM **and** LXC | A cloud-init template cloned end to end without the web UI | ✅ 8/8 — template 9000 built, cloned to 212, clone reachable over SSH; unprivileged container 120 created, cloned and destroyed; `vm rm` refuses a `managed` guest |
| **M4** ACL & security | Users, tokens, ACLs, diagnosing a 403 | A `403` provoked, diagnosed, fixed by ACL — not by escalation | ✅ 5/5 — throwaway token with no ACL → `403`; `whoami --can` says why; one targeted ACL on `/vms/120` fixes it; token revoked |
| **M5** Backup & DR | vzdump, restore, timed disaster-recovery drill | A destroyed VM restored, RPO/RTO measured | ✅ 4/4 — VM 212 backed up, destroyed, restored, service answering again. **RPO 19 s, RTO 20 s**, both measured, and what the archive did not hold written down |
| **M6** IaC | Dynamic inventory, drift detection, Terraform/Ansible wrappers | `iac drift` catches a change made outside Terraform | ✅ 8/8 — Terraform created VM 210 in 23 s, `iac inventory` found its address through the guest agent, Ansible deployed **native Nginx on :80 and containerised Caddy on :8080**, both idempotent on the second pass and both verified on their **body**, not their status code. An out-of-band `memory=3072` was caught by `iac drift` and resorbed by `iac apply` |
| **M7** Polish | Network, pools, migration, completion, CI, release | Binary installed and usable from the node | ✅ 7/7 — `net ls` shows pending changes read from **outside** `data`; pools created and emptied; a 64 MiB ISO fetched **by the node** with its checksum enforced, and a local file pushed by multipart; `migrate` explains what a single node cannot do; dynamic completion at `Tab`; CI failing under 70 % coverage; **and the binary answering `doctor` from the node itself, over `https://localhost:8006`** |
| **M8** Rename | `pvectl` → `pvecli`, everywhere the code names itself | Suite green at every step; the PVE token `automation@pve!pvectl` deliberately **not** renamed — it is an identity on the node, with ACLs attached, not a name this tool gets to choose | ✅ — `doctor` still returns four ✓ against the live node |
| **M9** Service catalogue | `vm declare`, embedded catalogue, Ansible roles, connection block | A VM declared in one command, built, resized and verified without writing a line of HCL | ✅ — VM 220 built in **18 s**, docker 29.7.1 + PostgreSQL 17.10 installed, second Ansible pass at `changed=0`, then grown to **16 GiB / 25 GiB** by re-declaring and re-applying, verified from the API *and* from inside the guest. Two collisions found only by running it against the real lab repo: a `site.yml` and a `requirements.yml` it would have overwritten |
| **M10** Cloudflare | Tunnels, ingress table, DNS, `cloudflared` role | A service reachable from the web with no port opened | ✅ — *remotely-managed* tunnels, so the ingress table lives in the API rather than in a `config.yml` inside the guest; proxied CNAME; `cloudflared` Ansible role. The model is **outbound**: nothing listens, no port is opened on the box |
| **M11** Delegated access | `access user create`, `vm create --pool`, `cf access` apps/policies/service tokens, `--no-tls-verify` | Someone else creates, resizes and destroys **their** VMs, from the internet, without seeing the rest of the lab | 🟡 code and tests done — the live proof waits on the sequence being played against the lab: `access user create` → `pool create` → three ACLs → `cf access app/policy/token` → `cf route add`. Measured on 08-03: the ACL step returns `403 (/access/acl, Permissions.Modify)` for the `pvectl` token, so this sequence needs an `Administrator` identity too |
| **M12** Bootstrap & secret | `pvecli login`, three secret sources, `auth set-secret\|status`, auto-update timer | The first token minted without SSHing to the node, and its secret reachable without exporting anything | ✅ — `automation@pve!pvectl-cc` minted by `pvecli login` on 2026-08-01; on 08-02 its secret was reached through `secret_command` on the Linux box with no environment variable exported, and `doctor` went green again |
| **M13** Operations | `vm agent exec`, `lxc exec`, `lxc firewall`, `fw ipset`, `backup job`, `access role add` | The guests operated from the API alone — no SSH, no `pct`, and the backup that will exist on the day of the outage is the *scheduled* one | 🟡 code complete — `lxc exec` and `lxc firewall` verified live against LXC 221 (`policy_in DROP`, 5432/7700 allowed from one address); `backup job ls` answers against the node and shows **no scheduled job at all**, which is the finding that motivated the lot. Job **writes** need `Sys.Modify` on `/`, which only `Administrator` carries among the built-in roles — `access role add` (PVX-077) shipped on 08-02 to mint the custom role that grants it *without* handing over the node — but on 08-03 that command **hit the same wall**: creating a least-privilege role itself needs `Sys.Modify` on `/access`, and `PVEAdmin`, which `pvecli login` attaches by default, does not carry it. Closing M13 needs an `Administrator` identity the tool does not mint |
| **—** Since M13 | `caddy` in the catalogue, `storage def`, `node reboot`, API-MAP coverage by (method, path) | — | ✅ — PVX-082 → 085, delivered 08-02 → 08-03, not yet gathered into a lot |

Full backlog: [`stories/`](stories/) — M0 → M7 story by story, each with
acceptance criteria, a proof command, and the thing it is supposed to teach;
everything after M7 lives in [`stories/BACKLOG.md`](stories/BACKLOG.md).
Product requirements: [`prd.md`](prd.md) — frozen at the v1 scope (M0 → M7).

## Stack

Go · [Cobra](https://github.com/spf13/cobra) · Proxmox VE 9.x REST API ·
Terraform ([`bpg/proxmox`](https://github.com/bpg/terraform-provider-proxmox)) ·
Ansible

Learning material this project follows:
[MakFly/proxmox-practice-lab](https://github.com/MakFly/proxmox-practice-lab).

## License

MIT
