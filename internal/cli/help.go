package cli

import "fmt"

func ShowHelp() {
	fmt.Print(`usage: ru [action] [-nEzfBm] [-L <label>] [-D <id,...>] [-W <id,...>]
                  [-N <num>] [-O <file>] [-P <num>] [cmd...]

Actions:
  (default)           Open the interactive TUI (jobs & sessions view)
  [cmd...]            Queue a command
  -K                  Kill the server
  -C                  Clear finished jobs
  -l                  List jobs to stdout
  -t [id]             Tail output of a job
  -c [id]             Cat (show all) output of a job
  -o [id]             Show output file path
  -p [id]             Show PID of a running job
  -i [id]             Show detailed info about a job
  -s [id]             Show state of a job
  -r [id]             Rerun a job (re-enqueue the same command)
  -x [id]             Remove a job from the queue
  -w [id]             Wait for a job to finish
  -k [id]             Kill a running job
  -T                  Kill all running jobs
  -u [id]             Make a queued job urgent (move to front)
  -U <id-id>          Swap two jobs in the queue
  -P [num]            Set or show max simultaneous job slots
  -a [id]             Show job label
  -F [id]             Show full command
  -q                  Show last queued job ID
  -R                  Count running jobs
  -S, tui             Open the interactive TUI (jobs & sessions view)
  -j, --jobs          Open the TUI directly in jobs-only view

Sessions:
  session list         List all sessions
  session create <n>   Create a new session
  session rename <o> <n> Rename a session
  session delete <n>   Delete a session (finished jobs only)
  session move <s> <g> Move a session to a group

Groups:
  group list           List all groups
  group create <n>     Create a new group
  group rename <o> <n> Rename a group
  group delete <n>     Delete an empty group

Modifiers (for job submission):
  -n                  Don't store output
  -E                  Separate stderr into .e file
  -z                  Gzip output
  -f                  Run in foreground (wait for completion)
  -m <message>        Attach a message to the job (shown by -i and -c)
  -B                  Non-blocking: exit with code 2 if queue full
  -L <label>          Assign a label to the job
  -N <num>            Require num slots for the job
  -O <file>           Custom log filename
  -d                  Depend on the last queued job
  -D <id,...>         Depend on specific job(s)
  -W <id,...>         Depend on specific job(s) succeeding
  -g <session>        Assign job to a session (default: "default")
  --session <session> Same as -g
  --timeout <dur>     Kill the job after this wall-clock time (e.g. 30m, 90s)
  --retries <n>       Re-run the job up to n extra times if it fails

Serialization:
  -M <format>         Output format: default, json, tab
  --serialize <fmt>   Same as -M

Configuration:
  config [list]        Show every setting: value + source (default/file/env/runtime)
  config get <key>     Print one setting's effective value
  config set <key> <v> Write a setting to the config file (applies live when possible)
  config edit          Open the config file in $EDITOR
  config path          Print the config file path

Environment:
  --getenv <var>      Get server environment variable
  --setenv <var=val>  Set server environment variable
  --unsetenv <var>    Unset server environment variable

Log directory:
  --get_logdir        Show log directory
  --set_logdir <path> Set log directory

Other:
  gc                  Delete orphaned job-output files from the log directory
  upgrade             Stop a stale daemon from an older binary (only if idle)
  -V                  Show version
  -h                  Show this help

Environment variables:
  QRUSH_SOCKET        Socket path (default: $TMPDIR/qrush-socket.<uid>)
  QRUSH_SLOTS         Initial max slots (default: 1)
  QRUSH_LOGDIR        Directory for job output files (default: $TMPDIR)
  QRUSH_MAXFINISHED   Max finished jobs to keep
  QRUSH_MAXCONN       Max client connections
  QRUSH_ONFINISH      Command to run on job completion
  QRUSH_SAVELIST      File to persist job queue
Legacy TS_* names are still accepted as fallbacks. The same settings can live
in ~/.config/qrush/config (see 'ru config'); precedence is runtime overrides
(settings changed live) > environment > config file > defaults.
`)
}

func ShowVersion() {
	fmt.Println("qrush v0.1.0 — Queue Rush")
}
