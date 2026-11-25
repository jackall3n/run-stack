---
name: setup-stack
description: Make a repository runnable with run-stack by writing its run-stack.toml and changing its dev scripts to take their ports from the environment. Use when asked to set up, add or configure run-stack for a repo, when run-stack reports that a folder has no run-stack.toml, or when `run-stack ports` shows undeclared ports.
---

# setup-stack: make a repo runnable with run-stack

The goal: two checkouts of this repo run at the same time and never share a port. That holds only if every listening port comes from a variable run-stack assigns.

## 1. Find every port

Read the dev command and everything it starts (package scripts, turbo or compose tasks, Procfiles, framework configs). List each process that listens, including the ones people forget: debugger and inspector ports, HMR websockets, stubs and mocks, local emulators, admin UIs. Note which ports the browser needs and which are only used between processes.

## 2. Make each one configurable

Change each script or config to read its port from an env variable, keeping today's number as the default so the repo still runs without run-stack:

```
"dev": "wrangler dev --port ${JOBS_PORT:-8787} --inspector-port ${JOBS_INSPECTOR_PORT:-9229}"
```

- A dev server that moves to the next port when its own is taken must be told not to (Vite: `strictPort: true`), or the URL would point at nothing.
- Replace hardcoded `localhost:<port>` references between services with env variables too.
- Dev servers that check the Host header need the stack's host allowed (Vite: `server.allowedHosts: [".local"]`).
- A port that cannot be configured at all cannot be isolated on the host: tell the user, and leave that service in a container.

## 3. Write run-stack.toml

`run-stack init` writes a commented starter at the checkout root. Fill it in:

```toml
name = "shop"
command = "bun run dev"
copy = [".env"]                      # untracked files a new worktree copies from the main checkout

[ports]
WEB_PORT = { route = "" }            # https://shop.local, https://<worktree>.shop.local
JOBS_PORT = { route = "jobs" }       # https://jobs.<host>
JOBS_INSPECTOR_PORT = { optional = true }   # a number, no URL, not waited for

[env]
APP_URL = "{url}"
COOKIE_DOMAIN = "{host}"
JOBS_URL = "http://localhost:{JOBS_PORT}"
```

- A port gets `route` only if a browser or webhook opens it. `host_header = "localhost"` is for servers that refuse any other Host.
- `[env]` holds whatever differs per stack: public URLs, cookie and auth domains, service-to-service URLs. Placeholders: `{host}` `{url}` `{url:PORT_NAME}` `{PORT_NAME}` `{id}` `{name}` `{worktree}` `{db}` `{dir}` `{main}`.
- Service-to-service URLs use `http://localhost:{PORT}`: it skips the proxy and needs no certificate. The public host also works from any program while the stack runs.
- The default domain is `<name>.local`. If something else on the machine already serves that name (an OrbStack container domain, for example), set `domain` to another one.
- These values win over `.env` files only if the loader does not override the environment (dotenv and Vite do not). Values a tool reads from its own file, such as wrangler's `.dev.vars`, need passing another way.

## 4. Databases: ask, don't assume

By default leave `DATABASE_URL` alone: every stack then uses the host's shared database, the one the user's database tools point at. Give worktrees their own database only if the user wants it, on the same server:

```toml
[env]
DATABASE_URL = "postgresql://localhost:5432/{db}"

[hooks]
pre_up = ["createdb {db} 2>/dev/null || true", "bun run db:migrate"]
```

`{db}` is the stack id with underscores. Never add a hook that drops or overwrites a database.

## 5. Prove it

`run-stack up -d && run-stack wait`, then `run-stack ports`: no line may say `UNDECLARED`. If the repo has a second worktree, start it too and check both answer. Stop what you started with `run-stack down`.
