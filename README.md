# gtach

Run a command in a persistent PTY, detach, and attach again without stopping it. Pure Go, with a CLI and an importable library. Linux and macOS; no C compiler, cgo, external dtach binary, terminal emulator, or scrollback.

Inspired by [dtach](https://github.com/crigler/dtach) by Ned T. Crigler. This is an independent implementation, not a source translation or a wire-compatible replacement. gtach clients connect only to gtach sessions.

## Disclaimer

This project was developed heavily with AI assistance. Treat the code as untrusted until you have reviewed and tested it for your use case. It has not had an independent security audit. No warranty is provided.

## Install

Install with Go 1.25 or newer:

```sh
go install github.com/pkar/gtach/cmd/gtach@latest
```

Download a binary from [releases](https://github.com/pkar/gtach/releases), or inspect and run the installer:

```sh
curl -fsSLO https://raw.githubusercontent.com/pkar/gtach/main/install.sh
sh install.sh
```

The installer verifies SHA-256 checksums for Linux amd64/arm64 and macOS arm64 binaries. On other Linux/macOS targets it builds the same release tag with Go. Set `GTACH_INSTALL_DIR` to choose the install directory. Checksums detect corruption; they are not independent signatures.

Build locally with `make`, or install with `make install PREFIX="$HOME/.local"`.

## CLI

For 0.0.2 (in development), run `gtach` with no arguments in any directory. It starts your `$SHELL` interactively, falling back to `/bin/sh` if `SHELL` is unset or empty. Press Ctrl-\ to detach, then run bare `gtach` from the same directory to resume that shell with its state intact. Use `exit` or Ctrl-D to end the shell; the next invocation starts a new session. Use `gtach --help` for usage.

Each resolved directory path gets one session per user. Symlink aliases share a session; different directories get separate sessions. Changing directories inside the session does not change its identity, and renaming the original directory gives it a new identity. Automatic sockets live under `/tmp/gtach-<uid>/`, not in the project, in a directory owned by you with mode 0700. Directory paths are hashed to keep socket names short. Existing unsafe directories and stale sockets are refused, not replaced.

For explicit session names, create a private socket directory and start a shell:

```sh
mkdir -p "$HOME/.gtach"
chmod 700 "$HOME/.gtach"
gtach -A "$HOME/.gtach/shell" "$SHELL"
```

Press Ctrl-\ to detach. Reattach with `gtach -a "$HOME/.gtach/shell"`. Multiple clients can attach and type into the same session. The last resize wins.

| Mode | Behavior |
| --- | --- |
| no arguments | Start or resume the current directory's shell (0.0.2) |
| `-a SOCKET` | Attach to an existing session |
| `-A SOCKET [COMMAND ...]` | Attach, or create and attach if absent |
| `-c SOCKET COMMAND ...` | Create and attach; fail if the path exists |
| `-n SOCKET COMMAND ...` | Create detached and return after startup |
| `-N SOCKET COMMAND ...` | Create detached with the server in the foreground |
| `-p SOCKET` | Copy stdin verbatim to the command |

For explicit modes, place options after the socket and before the command. Use `--` to end options. Arguments after the command are passed through unchanged; no shell is inserted.

| Option | Behavior |
| --- | --- |
| `-e '^A'` | Change the detach key; default is `'^\'` |
| `-E` | Disable detach-key processing |
| `-z` | Send Ctrl-Z to the command instead of suspending the client |
| `-r none` | Do not request a redraw |
| `-r ctrl_l` | Send Ctrl-L only in noncanonical, no-echo mode; default |
| `-r winch` | Signal the PTY foreground process group with SIGWINCH |
| `--version` | Print the binary version; use without a mode |
| `--help`, `-h` | Print usage without starting a session |

By default Ctrl-Z suspends the client and restores the local terminal. Resume it with your shell's `fg`. Ctrl-C is forwarded to the PTY. Closing the client or losing its terminal leaves the command running. Exit the command normally to end the session; send SIGTERM to a foreground `-N` server to terminate it.

```sh
gtach -n "$HOME/.gtach/job" sh -c 'exec ./long-running-job'
printf 'echo hello\n' | gtach -p "$HOME/.gtach/shell"
gtach -a "$HOME/.gtach/shell" -e '^A' -r winch
```

Interactive attach requires a TTY on stdin. `-n`, `-N`, and `-p` do not. `-N` returns the command's success/failure; an attaching client returns success when the connection ends, not the remote command's exit status.

## Library

```go
package main

import (
    "context"
    "io"
    "log"
    "os"
    "path/filepath"

    "github.com/pkar/gtach"
)

func main() {
    dir, err := os.MkdirTemp("", "gtach-") // mode 0700
    if err != nil { log.Fatal(err) }
    defer os.RemoveAll(dir)
    socket := filepath.Join(dir, "session")
    session, err := gtach.Start(context.Background(), gtach.Options{
        Socket: socket,
        Command: []string{"sh", "-c", "printf 'hello\\n'"},
        WaitForClient: true,
    })
    if err != nil { log.Fatal(err) }
    defer session.Close()
    client, err := gtach.Dial(context.Background(), socket)
    if err != nil { log.Fatal(err) }
    defer client.Close()
    if err := client.Attach(); err != nil { log.Fatal(err) }
    if _, err := io.Copy(os.Stdout, client); err != nil { log.Fatal(err) }
    if err := session.Wait(); err != nil { log.Fatal(err) }
}
```

`Start` runs in the hosting process, not a daemon. Keep that process alive to preserve the session. Use the context or `Session.Close` to terminate it, and `Session.Wait` to get the command's exit error. `Close` is idempotent and returns the same exit error as `Wait`, including an error when it kills the command.

`Client` implements `io.ReadWriteCloser`. `Dial` alone does not subscribe to output; call `Attach` before reading, or just `Write` and `Close` to push input. `Resize(rows, cols)` updates the shared PTY and `Redraw` requests a refresh. Closing a client does not close the session. The Dial context controls connection establishment only; close the client to interrupt later I/O.

## Boundaries

The socket's immediate parent must exist, belong to the current uid, and have mode 0700. Keep all ancestor directories trusted too. The socket is mode 0600. Anyone who can connect has full access to the session, including command input; this is not a multi-user security boundary.

Existing paths, including stale sockets, are never removed during startup. Confirm a stale session is dead before removing its socket yourself. Normal shutdown removes the socket. Unix socket path-length limits apply.

Output while detached is discarded. `-c` and newly created `-A` sessions preserve initial output until the first client attaches. A library session with `WaitForClient` also waits if the command has already exited; cancel or close it if no client will attach. Slow clients are disconnected when their 256 KiB output queue fills or a write stalls for one second, rather than blocking the command or other clients. Final output drains for up to one second after command exit and queued socket output for up to one more second.

There is no dtach socket interoperability, socket executable-bit status indicator, automatic stale-socket deletion, long-path workaround, or terminal-state replay. Redraw is selected per client. Terminating the server kills the command's original process group and closes the PTY; commands that deliberately escape into separate sessions are not supervised.

The pure-Go dependencies are `creack/pty`, `x/sys`, and `x/term`. Building with `CGO_ENABLED=0` is tested. Windows is not supported.

## Verify and release

```sh
go test -race ./...
CGO_ENABLED=0 go test ./...
go vet ./...
```

CI runs on Linux and macOS. The release process follows [pkar/schain](https://github.com/pkar/schain): push a `v*` tag, pass tests on both OSes, cross-build stripped CGO-disabled binaries, generate `checksums.txt`, and publish a GitHub release with generated notes. Library versions use the same tags.

After merging and verifying main, create and push the next semver tag:

```sh
git switch main
git pull --ff-only
git tag v0.0.2
git push origin v0.0.2
```

MIT licensed. dtach remains a separate GPL-licensed project.
