---
name: run-stack
description: Run a checkout folder (the main checkout or a git worktree) as its own local stack with the `run-stack` CLI, on its own ports and https://<worktree>.<name>.local URLs, so several run side by side. Use when asked to run, start, test or open an app, worktree, branch or folder locally, or to list running stacks, see their URLs, ports or logs, or open the dashboard. To stop one, use kill-stack. If the repo has no run-stack.toml yet, use setup-stack first.
argument-hint: "[dir] [ls|urls|logs|ports]"
---

# run-stack: run a folder as its own local stack

Arguments: `[dir]`, optional; it defaults to the checkout this session works in. With `ls`, `urls`, `logs` or `ports` as the first word, do that instead.

Everything goes through the `run-stack` CLI, run from inside the folder (`cd <dir>` is how you pick one). If `run-stack` is not on the PATH, install it with `curl -fsSL https://raw.githubusercontent.com/jackall3n/run-stack/main/install.sh | sh`: it puts the binary in `~/.local/bin`, where the user's own terminal finds it too. The same command upgrades it. If the folder has no `run-stack.toml`, use the `setup-stack` skill.

1. `run-stack up -d` starts the stack in the background and prints its URLs and ports. Show them to the user.
2. `run-stack wait` returns once every declared port answers (give the Bash call a long timeout; a first start may install dependencies). If it fails, read `run-stack logs` and report what went wrong.
3. `run-stack ports` lists what the stack really listens on. If anything is `UNDECLARED`, tell the user: a second stack of the same repo would collide on it.
4. Tell the user it is ready, with the URLs again.

The CLI works out whether the folder is the main checkout or a worktree, the stack's id, its ports and its URLs. Trust it: never derive or guess a port or URL, only repeat what it prints. Ports are different for every stack, so never assume `localhost:3000`.

- Every stack on the machine: `run-stack ls`. The same in the browser, with logs and start/stop buttons: `https://stack.local` (`run-stack dash` opens it).
- This folder's URLs without starting anything: `run-stack urls`.
- Logs: `run-stack logs` (last 200 lines) or `run-stack logs -f` in the background.
- A one-off command with the stack's ports and env (migrations, seeds, tests against it): `run-stack exec -- <command>`.

## What to know

- A stack's `.local` hosts resolve to this machine in every program while it runs, and stop resolving when it stops. Between a stack's own processes, `http://localhost:<port>` with the port variables from `run-stack env` is still the simpler address: no certificate involved.
- With curl, pass `--cacert ~/.run-stack/ca/rootCA.pem` unless the user has run `run-stack trust`. Never run `run-stack trust` yourself: it changes the user's keychain and asks for their password.
- The stack's env wins over `.env` files. After changing `run-stack.toml` or a `.env`, restart: `run-stack down && run-stack up -d`.
- A 502 page from a stack URL means the proxy is fine and that port is not answering yet: check `run-stack logs`.
- Stop a stack you started once the user no longer needs it, and before you delete its worktree.
