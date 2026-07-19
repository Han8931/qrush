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
- **Interactive TUI** — bare `ru` opens a full-screen **management home**: a collapsible group → session → job list with live status, an output pager, and a hardware status bar (CPU/memory/load)
- **Open sessions on demand** — open a session from the session picker (`S`, or `Enter` on an empty session's row) to drop into its tmux-style shell panes; `Ctrl+B d` detaches back to the management screen (shells persist in the daemon)
- **tmux-style panes** — split a session's terminal into tiled shells (`Ctrl+B |`/`-` or `:vs`/`:hs`), nest them, and navigate with the `Ctrl+B` prefix
- **Vim key bindings** — `j`/`k` to move, `h`/`l` to collapse/expand, `Ctrl+W` + `hjkl` to move focus between panes; `e` to edit the job/session under the cursor, `n` to create sessions; job actions (kill/remove/rerun/urgent) inline
- **Mouse support** — click to select/open rows on the management screen (disabled inside panes so terminal text-selection still works)
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

# List all jobs (to stdout)
ru -l
# 0    finished     0s      echo hello world
# 1    running      3s      sleep 10

# Open the interactive management TUI (bare `ru`, no args)
ru

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
| *(default)* | Open the interactive management TUI (jobs & sessions) |
| `-l` | List all jobs to stdout |
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
| `-S`, `tui` | Open the interactive management TUI (same as bare `ru`) |
| `-j`, `--jobs`, `tui -j` | Open the TUI directly in jobs-only mode (`q` quits) |
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

### Interactive TUI (`ru`)

Bare `ru` opens a full-screen **management home**: a flat, live-updating table of
every job across all sessions, with **Group** and **Session** columns. A detail
pane beneath the table shows the selected job in full, and a hardware status bar
along the bottom shows system-wide CPU, memory, load average, and core count
(updated every second).

```
╭───────────────────────────────────────────────────────────────────╮
│   ID GROUP ▲   SESSION    STATE      TIME    COMMAND               │
│    0 default   build      finished   0s      make all             │
│    1 default   build      running    4s      make test            │
│ ·    default   default    no jobs            — empty session · ⏎   │
│ ·    work      deploy     no jobs            — empty session · ⏎   │
│ ── job details ───────────────────────────────────────────────── │
│ Command: make all   State: finished   Real time: 0s               │
╰───────────────────────────────────────────────────────────────────╯
 MANAGE  sort:group▲  sessions 3  run 1  queue 0  done 1  fail 0  …
 HW   CPU 4%  MEM 2.6G/15.0G           load 1.11 1.35 1.13   12 cpu
```

Every session is listed: a session's jobs appear as rows, and a session with no
jobs still gets one placeholder row (so nothing is hidden). Press **Enter** on a
job row to open its **output** in the pager; on an empty-session row it drops into
that **session** — a tmux-style split of persistent shells (see below). Sessions
are also always reachable via the **session picker** (`S`). `Ctrl+B d` detaches
back to this screen. The bottom stat row shows session
and job counts; the status bar shows the current **mode**: `MANAGE` (the table),
`INSERT` (typing in a shell pane), or `COMMAND` (a `:` prompt is open); inside a
session it also shows the focused pane's git **branch** (`⎇ <branch>`).

Bare `ru` and `ru -S` both open this screen; `ru -j` opens it in **jobs-only**
mode, and `ru -l` still just prints the job list to stdout. `q` quits from this
screen (daemon-hosted shells keep running); `Esc` returns to the open session.

Only one interactive `ru` runs per daemon (like `tmux attach -d`): starting a
second one takes over — the earlier instance exits with a "detached" notice
instead of both mirroring the same shells and fighting over their sizes.
Running `ru` from *inside* a qrush pane doesn't take over; it surfaces the
already-running TUI's management screen. Mouse clicks select rows on this screen (mouse is
released inside panes so terminal text-selection still works). Press `?` any time
for a key/command cheatsheet.

**Management key bindings:**

| Key | Action |
|-----|--------|
| `j`/`k`, `gg`/`G`, `Ctrl+d`/`Ctrl+u` | Move / jump / half-page |
| `Space` | Select the cursor job and move down (ranger/lf-style); selected rows stay highlighted, `Esc` clears |
| `Enter` | Open the cursor job's **output** (the pager); on an empty-session row, open that session's shell panes |
| `o` | Open the output pager (scrollable; follows running jobs); press `i` there to overlay job info |
| `S` | Open the **session picker** — lists every session, incl. empty ones |
| `e` | Edit the cursor row in one box — a job row: job name + its session's name/group; a session row: name + group |
| `n` / `a` | Create a new session (opens the edit box; you land in it) |
| `sg` / `si` / `ss` / `st` | Sort by group / id / state / time; the same chord again (or `R`) reverses |
| `/` | Filter by command/label/session (`Enter` apply, `Esc` cancel) |
| `:` | Command line (see below) |
| `?` | Show the help overlay |
| `V` | Toggle visual mode; `j`/`k` extend the selection |
| `x` / `u` / `r` | Kill / make urgent / rerun the selected job(s) — the Space-selected set, the visual range, or the cursor row |
| `d` | Remove the selected job(s); finished jobs delete immediately, others confirm `y`/`n` |
| `D` / `C` | Delete all finished jobs (no confirm / confirm `y`/`n`) |
| `q` | Quit (daemon-hosted shells keep running) |
| `Esc` | Back to the open session (quits if none is open) |

#### Command line (`:`)

Press `:` to open a command line on the management screen:

| Command | Action |
|---------|--------|
| `:set slots <n>` (or `:slots <n>`) | Set the number of parallel job slots |
| `:set logdir <path>` | Set the daemon's log directory |
| `:sort <group\|id\|state\|time>` | Set the sort field |
| `:config` | Open the settings edit box (slots, log directory) |
| `:kill <id…>` / `:kill -a` | Kill the given jobs (no id: current selection) / all running jobs |
| `:restart <id…>` / `:restart -a` | Restart job(s): kill if running, then re-enqueue (no id: selection; `-a`: every non-queued job) |
| `:reset` | Factory-reset qrush — kills all jobs & panes, deletes sessions/groups, restores default settings (asks `y`/`n`) |
| `:clear` | Clear finished jobs |
| `:help` | Show the help overlay |
| `:q` | Quit |

#### Sessions & the session picker

A **session** is a named workspace with its own shells and its own jobs; sessions
are organized into **groups**. Every session shows in the table (empty ones as a
placeholder row, which `Enter` opens directly). The **session picker** (`S`)
gives a compact grouped list of every session — the way to open any session,
and handy for jumping around or reaching a session you just made:

```
╭─ Sessions ───────────────────────────────╮
│    default                               │
│  ▌ build             1 jobs              │
│    deploy            2 jobs              │
│    work                                  │
│    idle              0 jobs              │
│  ⏎:open · e:edit · n:new · d:delete · esc │
╰───────────────────────────────────────────╯
```

`e` (edit) and `n` (new) open a small modal to set a session's **name** and
**group**; submitting renames/moves or creates it. While the group field is
focused, the existing groups are listed in the box with the current match
highlighted — `Tab` steps through them, or type a new name to create a group
on save. On a **group header** row, `e` renames the group and `d` deletes it
(empty groups only, with confirmation).

#### Panes (tmux-style splits)

Inside a session the terminal can be split into multiple shells, tiled like tmux.
Each split spawns a fresh shell for that session; splits can be nested arbitrarily.
Commands are reachable two ways: the `Ctrl+B` prefix (press `Ctrl+B`, release,
then the key) and command mode (`Ctrl+B` then `c`, type the command, `Enter`).

The shells run inside the background daemon, so they **persist**: detach with
`Ctrl+B d` (or quit and reopen `ru`) and your panes, their layout, and any
still-running processes are reattached (recent scrollback is replayed). Panes
live until you close them (`Ctrl+B x`) or the daemon is stopped (`ru -K`).

| Prefix | Command mode | Action |
|--------|--------------|--------|
| `Ctrl+B` `|` / `%` | `vs` / `vsplit` | Split focused pane vertically (side by side) |
| `Ctrl+B` `-` / `"` | `hs` / `hsplit` / `split` | Split focused pane horizontally (stacked) |
| `Ctrl+B` `o` | `o` / `next` | Cycle focus to the next pane |
| `Ctrl+B` arrows / `h` `k` `l` | — | Move focus to the adjacent pane |
| `Ctrl+W` then `h`/`j`/`k`/`l` / `w` | — | Move / cycle focus between panes |
| `Ctrl+B` `x` / `Ctrl+W` `q` | `x` / `close` | Close the focused pane (last pane is kept) |
| `Ctrl+B` `d` | `detach` | Detach back to the management screen (shells persist) |
| `Ctrl+B` `j` | `jobs` / `j` | Jump to the management screen |
| `Ctrl+B` `c` | — | Open command mode |
| `Ctrl+B` `q` | `q` / `quit` | Quit |

The `Ctrl+B` prefix stays active until the next key. Press `Esc` after `Ctrl+B`
to cancel it. If the next key isn't a recognized chord, the buffered `Ctrl+B`
is forwarded to the focused shell, so shell shortcuts still work.

Activating a session sets `QRUSH_SESSION` in the embedded shell; `ru` uses it as
the default session when `-g` is omitted. Running `ru` (or `ru -j`) from *inside*
a pane won't nest a second TUI — it surfaces the already-running management screen
instead, so you never open a session inside a session. The embedded shell supports
vi mode (`set -o vi`) — all keystrokes are forwarded directly.

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

## Configuration

Settings are layered; later layers win:

```
built-in defaults  <  config file  <  environment variables  <  runtime overrides
```

The **config file** is `~/.config/qrush/config` (`$XDG_CONFIG_HOME` respected)
with plain `key = value` lines and `#` comments. **Runtime overrides** are
recorded automatically when you change a setting on a live daemon (`ru -P`,
`--set_logdir`, or the TUI's `:config` box), so those changes survive daemon
restarts; they outrank the environment because they capture your most recent
explicit choice.

```bash
ru config              # every setting: value + source (default/file/env/runtime)
ru config get slots
ru config set slots 4  # writes the file and applies to a running daemon
ru config edit         # open the file in $EDITOR (creates a template)
ru config path
```

| Key | Environment | Description | Default |
|-----|-------------|-------------|---------|
| `slots` | `QRUSH_SLOTS` | Max simultaneous job slots | `1` |
| `logdir` | `QRUSH_LOGDIR` | Directory for job output files | `$TMPDIR` |
| `socket` | `QRUSH_SOCKET` | Daemon socket path | `$TMPDIR/qrush-socket.<uid>` |
| `max_finished` | `QRUSH_MAXFINISHED` | Finished jobs to keep (`-1`: unlimited) | unlimited |
| `max_conn` | `QRUSH_MAXCONN` | Max client connections | `10` |
| `on_finish` | `QRUSH_ONFINISH` | Command to run on job completion | — |
| `save_list` | `QRUSH_SAVELIST` | Queue snapshot file (`none` disables) | `~/.local/state/qrush/queue.json` |

`QRUSH_SESSION` (environment only) sets the default session when `-g` is
omitted; the TUI sets it inside session shells. Legacy `TS_*` names are still
accepted as fallbacks for compatibility.

## Output Files

Each job's output is stored in `$TMPDIR/ru_<jobID>_<random>.out` (8 random hex chars). The random suffix prevents old log files from being overwritten when job IDs repeat across daemon restarts. Use `ru -o <id>` to print the exact path, `ru -c <id>` to view (or follow) the content.

When a job leaves the queue (`ru -x`, `ru -C`, `QRUSH_MAXFINISHED` pruning, or
deleting its session), its auto-generated output file is deleted with it, so
the log directory doesn't accumulate orphaned files. Files you named yourself
with `-O` are never deleted. `ru gc` sweeps the current log directory for
generated `ru_*.out` files that no live job references (leftovers from older
versions or unpersisted queues) — user-named files are never candidates.

## Architecture

`qrush` is a single binary that acts as both client and server:

- **Server**: A background daemon that manages the job queue, executes jobs, and communicates with clients via Unix domain sockets (Linux/macOS) or TCP on localhost (Windows). On Windows, connections must present a random token from the owner-only (0600) port file, so other local users can't drive your daemon.
- **Client**: Connects to the server to submit jobs, query status, and control execution.
- The server starts automatically on first client connection and runs until killed with `ru -K`.
- The daemon logs to `~/.local/state/qrush/daemon.log` (`$XDG_STATE_HOME`
  respected; rotated to `.old` past ~1MB) — startup info, config warnings, and
  runtime errors land there, since the detached daemon has no terminal.
- After upgrading `ru`, a daemon from an older binary is **never killed
  automatically**: commands refuse with a version message until you run
  `ru -K` yourself, so running jobs and live panes die only when you decide.

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
