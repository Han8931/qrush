# qrush — Queue Rush

A cross-platform task spooler written in Go. Inspired by [task-spooler](https://github.com/justanhduc/task-spooler).

`qrush` queues commands for sequential (or parallel) execution. Submit jobs from any terminal, and they run in order on a background daemon that starts automatically. The command-line tool is called `ru`.

## Features

- **Job queue** — submit commands, they run in order
- **Concurrency control** — configurable number of parallel slots
- **Job dependencies** — run a job only after another finishes (or succeeds)
- **Output capture** — stdout/stderr saved to files, viewable with `-c` (follows running jobs like `tail -f`) or tailed live with `-t`
- **PTY execution** — commands run attached to a pseudo-terminal so output stays line-buffered (no buffering surprises for long-running jobs)
- **Labels & messages** — tag jobs with `-L`, attach a free-form note with `-m` (shown by `-c` and `-i`)
- **Sessions** — group jobs into named sessions for organization
- **Interactive TUI** — split-pane terminal with NERDTree-style session tree (`ru -S`)
- **tmux-style panes** — split the terminal into tiled shells (`Ctrl+B |`/`-` or `:vs`/`:hs`), nest them, and navigate with the `Ctrl+B` prefix
- **Job-management view** — full-screen, live, vim-keybound job table (`Ctrl+B j` / `:jobs`) with kill/remove/rerun/urgent actions, a built-in output pager, and a system hardware status bar (CPU/memory/load)
- **Vim key bindings** — tree navigation with j/k, `Ctrl+W` + `hjkl` to move focus between the tree and panes, `,n` to toggle tree
- **Cross-platform** — Linux, macOS, Windows (single binary)
- **Auto-start** — server daemon starts on first use, no setup needed
- **JSON output** — machine-readable job listing

## Installation

```bash
# From source
go install github.com/han/qrush/cmd/ru@latest

# Or build locally
git clone https://github.com/han/qrush.git
cd qrush
make build      # builds ./ru in the project directory
make install    # installs ru to $GOPATH/bin
make uninstall  # removes ru from $GOPATH/bin
```

## Quick Start

```bash
# Submit a job
ru echo "hello world"
# 0

# Submit another
ru sleep 10
# 1

# List all jobs
ru
# 0    finished     0s      echo hello world
# 1    running      3s      sleep 10

# View output of a finished job
ru -c 0
# hello world

# Tail a running job's output
ru -t 1

# Wait for a job to finish
ru -w 1
```

## Usage

```
ru [action] [-nEzf] [-m <msg>] [-L <label>] [-D <id,...>] [-W <id,...>]
                  [-N <num>] [-O <file>] [-P <num>] [-g <session>] [cmd...]
```

### Actions

| Flag | Description |
|------|-------------|
| *(default)* | List all jobs |
| `[cmd...]` | Queue a command |
| `-t [id]` | Tail output of a job (follows live) |
| `-c [id]` | Show output of a job (follows running jobs until they finish; shows exit code if non-zero) |
| `-o [id]` | Show output file path |
| `-p [id]` | Show PID of a running job |
| `-i [id]` | Show detailed info about a job |
| `-s [id]` | Show state of a job |
| `-r [id]` | Rerun a job (re-enqueue the same command; prints the new id) |
| `-x [id]` | Remove a job / delete it from the queue |
| `-w [id]` | Wait for a job to finish |
| `-k [id]` | Kill a running job |
| `-u [id]` | Make a queued job urgent (move to front) |
| `-T` | Kill all running jobs |
| `-U <id-id>` | Swap two jobs in the queue |
| `-P [num]`, `--slots [num]` | Set or show max simultaneous slots |
| `-a [id]` | Show job label |
| `-F [id]` | Show full command |
| `-q` | Show last queued job ID |
| `-R` | Count running jobs |
| `-C` | Clear finished jobs |
| `-K` | Kill the server |
| `-S`, `tui` | Open interactive session tree (TUI) |
| `-j`, `--jobs`, `tui -j` | Open the TUI directly in the job-management view |
| `term ls` | List live interactive panes (`session  pane`) hosted by the daemon |
| `term kill <session> <pane>` | Kill a persistent interactive pane |

### Job Submission Modifiers

| Flag | Description |
|------|-------------|
| `-n` | Don't store output |
| `-E` | Separate stderr into `.e` file |
| `-f` | Run in foreground (wait for completion) |
| `-m <message>` | Attach a message to the job (shown by `-i` and as a header in `-c`) |
| `-L <label>` | Assign a label |
| `-N <num>` | Require num slots |
| `-O <file>` | Custom log filename |
| `-d` | Depend on the last queued job |
| `-D <id,...>` | Depend on specific job(s) |
| `-W <id,...>` | Depend on specific job(s) succeeding |
| `-g <session>` | Assign job to a session (default: `"default"`) |
| `--session <s>` | Same as `-g` |

### Sessions

```bash
# List sessions
ru session list

# Create a session
ru session create build

# Submit a job to a session
ru -g build make all

# List jobs in a session only
ru -g build -l

# Rename / delete a session
ru session rename build ci
ru session delete ci

# Group sessions
ru group create work
ru session move ci work
ru group list
```

### Interactive TUI (`ru -S`)

Opens a split-pane terminal: session tree on the left, an embedded shell on the right.

```
┌──────────────┬────────────────────────────────┐
│ ▼ default    │ $ ru echo hello                │
│   default    │ 0                              │
│ ▶ work (2)   │ $ _                            │
├──────────────┴────────────────────────────────┤
│ INSERT default      ⎇ main  jobs 2  slots 1   │
└───────────────────────────────────────────────┘
```

The status bar shows the current **mode** — `NORMAL` (navigating the tree),
`INSERT` (typing in a shell pane), or `COMMAND` (a `:`/command prompt is open) —
and, when the focused pane's working directory is a git repository, the current
**branch** (`⎇ <branch>`) — it follows the pane as you `cd` around.

**Key bindings:**

| Key | Context | Action |
|-----|---------|--------|
| `,n` | Any | Toggle tree panel |
| `Ctrl+W` then `h`/`j`/`k`/`l` | Any | Move focus between the tree and adjacent terminal panes |
| `Ctrl+W` then `w` | Any | Cycle focus through the tree and every pane |
| `j`/`k` | Tree | Move cursor up/down |
| `gg` / `G` | Tree | Jump to top / bottom |
| `Enter`/`l` | Tree | Expand/collapse group or activate session |
| `h` | Tree | Collapse / go to parent |
| `Space` | Tree | Toggle selection for current row |
| `v` | Tree | Toggle selection for all visible rows |
| `a` | Tree | Create new session in selected group |
| `M` | Tree | Create new group |
| `A` | Tree | Toggle wide tree pane |
| `r` | Tree | Reset selected/current session shell |
| `d` | Tree | Delete selected/current session/group |
| `R` | Tree | Rename selected session/group |
| `m` | Tree (on session) | Move session to group |
| `q` | Tree | Quit (or switch to shell) |

#### Panes (tmux-style splits)

The terminal pane can be split into multiple shells, tiled like tmux. Each split
spawns a fresh shell for the current session; splits can be nested arbitrarily.
Commands are reachable two ways: the `Ctrl+B` prefix (press `Ctrl+B`, release,
then the key) and command mode (`Ctrl+B` then `c`, type the command, `Enter`).

The shells run inside the background daemon, so they **persist across `ru -S`
restarts**: quit the TUI and reopen it and your panes, their layout, and any
still-running processes are reattached (recent scrollback is replayed). Panes
live until you close them (`Ctrl+B x`), reset the session (`r`), or the daemon
is stopped (`ru -K`).

| Prefix | Command mode | Action |
|--------|--------------|--------|
| `Ctrl+B` `|` / `%` | `vs` / `vsplit` | Split focused pane vertically (side by side) |
| `Ctrl+B` `-` / `"` | `hs` / `hsplit` / `split` | Split focused pane horizontally (stacked) |
| `Ctrl+B` `o` | `o` / `next` | Cycle focus to the next pane |
| `Ctrl+B` arrows / `h` `k` `l` | — | Move focus to the adjacent pane (`j` opens the job view) |
| `Ctrl+B` `x` / `Ctrl+W` `q` | `x` / `close` | Close the focused pane (last pane is kept) |
| `Ctrl+B` `j` | `jobs` / `j` | Open the job-management view |
| `Ctrl+B` `c` | — | Open command mode |
| `Ctrl+B` `q` | `q` / `quit` | Quit |

The `Ctrl+B` prefix stays active until the next key. Press `Esc` after `Ctrl+B`
to cancel it. If the next key isn't a recognized chord, the buffered `Ctrl+B`
is forwarded to the focused shell, so shell shortcuts still work.

Activating a session sets `QRUSH_SESSION` in the embedded shell; `ru` uses it as the default session when `-g` is omitted. The embedded shell supports vi mode (`set -o vi`) — all keystrokes are forwarded directly.

#### Job-management view

Press `Ctrl+B j` (or run `:jobs` / `:j` in command mode) to open a full-screen,
live-updating table of jobs with vim keybindings. A fixed pane beneath the table
always shows full details for the selected job, and a hardware status bar along
the very bottom shows system-wide CPU, memory, load average, and core count
(updated every second). `q`/`Esc` returns to the split view.

You can also open it from a shell with `ru --jobs` (alias `ru -j`). Run from an
ordinary terminal it launches a standalone job table that `q`/`Esc` simply
closes. Run from *inside* a qrush pane it instead signals the already-running
interactive session to switch to its jobs view (rather than nesting a second TUI
on top of itself).

| Key | Action |
|-----|--------|
| `j`/`k`/`Space`, `gg`/`G`, `Ctrl+d`/`Ctrl+u` | Move / jump / half-page |
| `a` | Toggle scope: all sessions ↔ active session |
| `/` | Filter by command/label/session (`Enter` apply, `Esc` cancel) |
| `Enter` / `o` | Open the output pager (scrollable; follows running jobs); press `i` there to overlay job info |
| `V` | Toggle visual mode; `j`/`k` extend the selection |
| `d` | Remove the selected job(s); finished jobs delete immediately, others confirm `y`/`n` |
| `r` | Rerun the selected job(s) as fresh jobs |
| `x` | Kill the selected job(s) |
| `u` | Make the selected job(s) urgent |
| `D` | Delete all finished jobs immediately (no confirmation) |
| `C` | Clear all finished jobs (confirm `y`/`n`) |
| `Esc` | Exit visual mode, or close the view |
| `q` | Close the view |

In the output pager: `j`/`k`, `gg`/`G`, `Ctrl+d`/`Ctrl+u` scroll, `/` then
`n`/`N` search, `i` toggles a job-info panel, `G` re-follows a running job,
`q`/`Esc` returns to the table.

### Examples

```bash
# Run 3 jobs in parallel
ru -P 3        # or: ru --slots 3

# Submit a labeled job
ru -L "build" make all

# Attach a message (like a git commit message)
ru -m "trying with -O3" make all
# ru -i 0 shows: Message: trying with -O3
# ru -c 0 prepends: # trying with -O3

# Chain jobs with dependencies
ru -L "build" make all        # job 0
ru -d -L "test" make test     # job 1, runs after job 0
ru -D 0,1 -L "deploy" ./deploy.sh  # runs after both 0 and 1

# Only run if dependency succeeded (exit code 0)
ru -W 0 echo "build passed!"

# Run in foreground (blocks until done)
ru -f make build

# JSON output for scripting
ru -M json

# Tab-delimited output
ru -M tab

# Submit jobs to different sessions
ru -g build make all
ru -g test make test

# Open interactive TUI
ru -S

# Server-side environment variables
ru --setenv MY_VAR=value
ru --getenv MY_VAR

# Change log directory
ru --set_logdir /tmp/my-logs
ru --get_logdir
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `QRUSH_SOCKET` | Socket path | `$TMPDIR/qrush-socket.<uid>` |
| `QRUSH_SLOTS` | Initial max slots | `1` |
| `QRUSH_MAXFINISHED` | Max finished jobs to keep | unlimited |
| `QRUSH_MAXCONN` | Max client connections | `10` |
| `QRUSH_ONFINISH` | Command to run on job completion | — |
| `QRUSH_SAVELIST` | File to persist job queue | — |
| `QRUSH_SESSION` | Default session when `-g` is omitted (set by the TUI on session activation) | — |

Legacy `TS_*` names are still accepted as fallbacks for compatibility.

## Output Files

Each job's output is stored in `$TMPDIR/ru_<jobID>_<random>.out` (8 random hex chars). The random suffix prevents old log files from being overwritten when job IDs repeat across daemon restarts. Use `ru -o <id>` to print the exact path, `ru -c <id>` to view (or follow) the content.

## Architecture

`qrush` is a single binary that acts as both client and server:

- **Server**: A background daemon that manages the job queue, executes jobs, and communicates with clients via Unix domain sockets (Linux/macOS) or TCP on localhost (Windows).
- **Client**: Connects to the server to submit jobs, query status, and control execution.
- The server starts automatically on first client connection and runs until killed with `ru -K`.

## Building

```bash
# Native build
make build

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o ru-linux ./cmd/ru/
GOOS=darwin GOARCH=arm64 go build -o ru-macos ./cmd/ru/
GOOS=windows GOARCH=amd64 go build -o ru.exe ./cmd/ru/
```

## ToDo

- HW monitoring popup box

## License

MIT
# qrush
