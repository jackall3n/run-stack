---
name: kill-stack
description: Stop a local stack started with run-stack, for the current checkout, a named stack or all of them. Use when asked to stop, kill or shut down a locally running app, worktree or stack, or before deleting a worktree whose stack is running.
argument-hint: "[id|--all]"
---

# kill-stack: stop a local stack

- This checkout's stack: `run-stack down`, from inside the folder.
- A named one, from anywhere: `run-stack ls` to see the ids, then `run-stack down <id>`. This also works when the stack's folder has been deleted.
- Everything: `run-stack down --all`. Only when the user asked for all of them; other sessions may be using the others.

`down` stops the command and every process it started, then runs the repo's `post_down` hooks. The stack keeps its port numbers for next time. A stack that `run-stack ls` shows as `orphaned` is stopped the same way.

To release a stopped stack's ports for good (for example after its worktree is deleted): `run-stack forget <id>`.

Never kill a stack's processes by pid or port yourself, and leave the shared proxy running: other stacks use it.
