# pvecli — a Proxmox VE CLI, built to learn the API

`pvecli` is a remote command-line client for **Proxmox VE**, written in Go. It
drives a homelab node through the `/api2/json` REST API — inventory, VM and LXC
lifecycle, tasks, storage, ACLs, backups — and bridges that API to a
Terraform / Ansible pipeline.

It is **not** a clone of `pvesh`. `pvesh` only exists *on* the node, behind SSH.
`pvecli` is a remote, typed, scriptable client with the guardrails the web UI
does not offer: `--dry-run` everywhere, JSON output, real task polling, generated
Ansible inventories.

> **Status: M0 through M10 are closed.** `pvecli` reads a
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
> port opened on the router.

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
3. the checksums are re-confronted with their files;
4. GitHub attests the provenance — a SHA-256 proves a file has not moved, it
   says nothing about where it came from.

```sh
gh attestation verify pvecli_v0.1.0_linux_amd64 --repo MakFly/pvecli
```

### First run

You need an API token. `root@pam` works and is exactly what this tool is built
to avoid — create a dedicated one instead (PRD Appendix A):

```sh
# On the node, once.
pveum user add automation@pve
pveum acl modify / --roles PVEAuditor --users automation@pve
pveum user token add automation@pve pvecli --privsep 1
# → note the secret. It is shown ONCE and never again.
```

The secret never goes in a file and never goes in a flag — `ps` and the shell
history both read flags. On macOS:

```sh
security add-generic-password -a pvecli -s pvecli-token -w '<le secret>'
export PVE_API_TOKEN_SECRET="$(security find-generic-password -a pvecli -s pvecli-token -w)"
```

Then, in order:

```sh
pvecli config init --endpoint https://pve.example:8006 \
    --token-id 'automation@pve!pvectl' --node pve
pvecli config trust     # pin the certificate — stronger than --insecure, costs one command
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
```

Environment variables — `PVE_API_URL`, `PVE_API_TOKEN_ID`,
`PVE_API_TOKEN_SECRET`, `PVE_INSECURE` — are named to stay interoperable with
the `pve-api` bash client from the reference lab.

**`token_secret` is rejected inside the config file**, with an error pointing at
the environment instead — at the line to delete, and wherever in the document it
was hiding. A config file is something you eventually commit.

`pvecli config show` prints the *effective* configuration together with the
layer each value won on, so the precedence is observable rather than assumed:

```
contexte      lab                        (fichier)
endpoint      https://autre:8006         (env PVE_API_URL)
node          pve                        (fichier)
token_secret  <défini>                   (env PVE_API_TOKEN_SECRET)
```

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
| **M10** Cloudflare | Tunnels, ingress table, DNS, `cloudflared` role | A service reachable from the web with no port opened | 🟡 code and tests done — the live proof waits on a Cloudflare API token |

Full backlog: [`stories/`](stories/) — 55 stories, each with acceptance
criteria, a proof command, and the thing it is supposed to teach.
Product requirements: [`prd.md`](prd.md).

## Stack

Go · [Cobra](https://github.com/spf13/cobra) · Proxmox VE 9.x REST API ·
Terraform ([`bpg/proxmox`](https://github.com/bpg/terraform-provider-proxmox)) ·
Ansible

Learning material this project follows:
[MakFly/proxmox-practice-lab](https://github.com/MakFly/proxmox-practice-lab).

## License

MIT
