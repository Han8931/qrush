---
name: verify
description: Build and drive qrush (ru) end-to-end on an isolated socket to verify daemon/CLI changes at the real surface.
---

# Verifying qrush changes

qrush is a single binary (`ru`) that is both client and auto-started daemon.
Verify by driving the CLI against a private daemon; never the user's default
socket.

## Build and isolate

```bash
D=$(mktemp -d)                      # short path — sun_path limit is 104 bytes on macOS
go build -o "$D/ru" ./cmd/ru
export QRUSH_SOCKET="$D/s.sock"     # daemon inherits env from the first client
```

Gotcha: set `D` and `QRUSH_SOCKET` in **separate** statements —
`export D=… QRUSH_SOCKET=$D/…` expands `$D` before assignment in zsh and you
end up dialing `/s.sock`.

## Drive

- First `"$D/ru" <cmd>` auto-starts the daemon (spawned from the same binary).
- Core flow: submit → `ru` (list) → `ru -o <id>` (path must exist on disk) →
  `ru -c <id>` (content) → `ru -t <id>` (follow).
- Concurrency probe: `ru -P 4`, then 40 backgrounded submissions, `wait`,
  confirm every `ru -o` path exists.
- Tail-follow probe: submit `sh -c 'sleep 2; yes A | head -c 200000'`, run
  `ru -t` during the sleep, compare byte count with `ru -c`. PTY converts
  `\n` → `\r\n`, so expect 1.5× the written bytes.
- Panes/TUI: `ru term ls`, or the integration test path via
  `client.OpenTerminal`/`AttachTerminal`.

## Clean up

Job output files land in `$TMPDIR/ru_<id>_<hex>.out` (shared with real usage —
delete only paths obtained from `ru -o`):

```bash
"$D/ru" -M tab | awk '{print $1}' | while read -r id; do
  p=$("$D/ru" -o "$id" 2>/dev/null) && rm -f "$p" "$p.e"
done
"$D/ru" -K            # kill daemon (removes socket)
rm -rf "$D"
```
