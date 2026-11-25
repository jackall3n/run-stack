# run-stack

Run any checkout, or several git worktrees of it, as local stacks side by side, without Docker and without port collisions.

- Every port a stack needs gets a free number, kept across restarts and passed to your dev command as env variables.
- The ports you route get HTTPS URLs: `https://shop.local` for the main checkout, `https://<worktree>.shop.local` for a worktree, `https://jobs.<host>` for a second service.
- Your processes run on the host, with the host's dependencies and tools.
- A dashboard at `https://stack.local` shows every stack, its ports and its logs, and can start and stop them.

One binary does all of it: the port bookkeeping, supervising the processes, the HTTPS proxy and the name resolution.

Built for macOS. On Linux everything works except `run-stack trust` and `.local` names: use a `.localhost` domain there, and give the binary permission to bind ports 80 and 443 (or pick others in `~/.run-stack/config.json`).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/jackall3n/run-stack/main/install.sh | sh
run-stack trust     # once: trust the local certificate authority (asks for your password)
run-stack doctor
```

The script downloads the binary for your system into `~/.local/bin` and checks it against the release's checksums. Run it again to upgrade. With Go installed, `go install github.com/jackall3n/run-stack@latest` works too.

As a Claude Code plugin (skills `run-stack`, `kill-stack`, `setup-stack`):

```
/plugin marketplace add jackall3n/run-stack
/plugin install run-stack@run-stack
```

The skills call the same `run-stack` binary you use in your terminal, and install it with the script above if it is missing. You and Claude see the same stacks: `run-stack ls` in either place lists them all.

## Use

In a repo, once: `run-stack init`, then edit `run-stack.toml`.

```toml
name = "shop"
command = "bun run dev"
copy = [".env"]

[ports]
WEB_PORT = { route = "" }                    # https://<host>
JOBS_PORT = { route = "jobs" }               # https://jobs.<host>
JOBS_INSPECTOR_PORT = { optional = true }    # a number, no URL

[env]
APP_URL = "{url}"
JOBS_URL = "http://localhost:{JOBS_PORT}"
```

Then, from the main checkout or any worktree:

```sh
run-stack up            # foreground; Ctrl-C stops it. -d to detach
run-stack wait          # until every port answers
run-stack ls            # every stack on the machine
run-stack dash          # the dashboard, https://stack.local
run-stack ports         # what it really listens on; flags ports nobody declared
run-stack logs -f
run-stack exec -- bun run db:migrate    # a command with the stack's env
run-stack down
```

The command must listen on the numbers it is given: change `--port 8787` to `--port ${JOBS_PORT:-8787}`. A port that is hardcoded and cannot be changed cannot be isolated on the host.

## The config

`run-stack.toml` sits at the root of a checkout, or in a folder inside it for one package of a monorepo.

| Key | Meaning |
| --- | --- |
| `name` | The stack's name. Defaults to the main checkout's folder name. |
| `command` | What to run, from the config's folder. Everything it starts is stopped with it. |
| `domain` | Defaults to `<name>.local`. A worktree is served at `<worktree>.<domain>`. |
| `copy` | Untracked files a worktree copies from the main checkout if it lacks them. |
| `[ports]` | One entry per listening port; the key is the env variable that carries the number. `route` gives it a URL (`""` for the host itself, `"jobs"` for `jobs.<host>`), `host_header` overrides the Host sent upstream, `optional` keeps `wait` from waiting for it. |
| `[env]` | Extra variables. Placeholders: `{host}` `{url}` `{url:PORT_NAME}` `{PORT_NAME}` `{id}` `{name}` `{worktree}` `{db}` `{dir}` `{main}`. |
| `[hooks]` | `pre_up` and `post_down`: shell commands run with the stack's env. A failing `pre_up` stops the start. |

## Good to know

- **Databases.** run-stack leaves `DATABASE_URL` alone, so by default every stack uses whatever your `.env` points at. For a database per worktree on the same server, set `DATABASE_URL = "postgresql://localhost:5432/{db}"` and create it in a `pre_up` hook.
- **`.env` files.** The stack's variables are in the process environment, so they win wherever the loader does not override it (dotenv and Vite do not).
- **Names.** While a stack runs, its `.local` hosts resolve to this machine in every program (browsers, curl, Node), with no sudo and no `/etc/hosts`. They stop resolving when it stops. Don't give a stack the same domain as something else that claims it, such as an OrbStack container domain.
- **Only this machine.** The proxy refuses connections from other machines, so stacks and the dashboard are not reachable from your network.
- **Certificates.** The authority lives in `~/.run-stack/ca`. Its key can sign for any site and is readable only by you. Firefox has its own store: import `rootCA.pem` there. Stacks get `NODE_EXTRA_CA_CERTS` pointing at it.
- **State.** `~/.run-stack/state.json` records every stack and its ports; logs are in `~/.run-stack/logs`. `~/.run-stack/config.json` can set `https_port`, `http_port` and `port_range`.

## Develop

Needs Go and [Task](https://taskfile.dev). `task` lists everything:

```sh
task check      # what CI runs: formatting, vet, tests, the build without cgo
task install    # build, install to ~/.local/bin, restart the proxy
task demo       # run examples/demo in the foreground
```

```
main.go                 entry point; the version is set here by the release build
internal/
  cli/                  the commands: argument parsing and printing, nothing else
  config/               run-stack.toml, and working out which stack a folder is
  state/                ~/.run-stack/state.json: every stack, its ports, whether it runs
  ports/                giving out port numbers; finding what a stack really listens on
  runner/               starting a stack's command, supervising it, stopping it
  proxy/                the shared HTTPS proxy, its routing and the dashboard
  certs/                the local certificate authority
  dns/                  making .local names resolve (macOS)
  home/                 where files live, and machine-wide settings
skills/                 the Claude Code skills
.claude-plugin/         the plugin and marketplace manifests
examples/demo/          a small stack to try things on
install.sh              the installer the README and the skills point at
```

Packages depend downwards only: `cli` on everything, `proxy` on `runner`, and nothing on `cli`.

**Releases.** Every push to `main` that changes Go code is released as the next patch version, with macOS and Linux binaries. For a minor or major version, push the tag yourself: `git tag v0.2.0 && git push --tags`.
