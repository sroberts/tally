# Developing tally

How tally is built and tested, and how changes land. The [README](../README.md) is for people using tally; this is for people changing it.

## Setup

- Go 1.27 or later. Node is optional: with it, `go test` also runs the browser pad's lighting tests.
- Python tooling (vibe-macropad's tests) uses [uv](https://docs.astral.sh/uv/) for environments.
- Install the local hooks that keep `main` clean (they live in `.git/hooks`, so each clone needs them):

  ```bash
  cat > .git/hooks/pre-commit <<'EOF'
  #!/bin/sh
  # Block commits on main. Work on a branch and merge through a PR (GitHub Flow).
  [ "$(git symbolic-ref --short -q HEAD)" = main ] && { echo "pre-commit: refusing to commit on main; git switch -c <branch>" >&2; exit 1; }
  exit 0
  EOF
  cat > .git/hooks/pre-push <<'EOF'
  #!/bin/sh
  # Block pushes that update main on the remote. Merge through a PR instead.
  while read -r _ _ remote_ref _; do
    [ "$remote_ref" = refs/heads/main ] && { echo "pre-push: refusing to push to main; push a branch and open a PR" >&2; exit 1; }
  done
  exit 0
  EOF
  chmod +x .git/hooks/pre-commit .git/hooks/pre-push
  ```

## How changes land

Work follows GitHub Flow:

1. **Branch** off an up-to-date `main` with a short kebab-case name (`web-pad`, `scrub-ios`). The hooks refuse commits and pushes on `main`.
2. **Commit** in small, complete steps. The subject is one plain sentence saying what changed (`Harden browser pad sign-in after review`), with no type prefix and no trailing period. The body says why, and lists the pieces when there are several.
3. **Push the branch and open a PR.** The description summarizes the change, names anything deliberately left alone, and says what was tested.
4. **Review before merging.** Each PR gets a code review. Findings are fixed on the branch, in their own commit, and the tests run again.
5. **Merge on GitHub with a merge commit**, never squash or rebase. Then pull `main` and, if you run tally from your build, reinstall it (below).

Tests are the contract. Fix the code to pass them; change an existing test only with the owner's explicit sign-off.

## Build, test, run

```bash
go build ./... && go vet ./...
go test ./...                                        # everything; -short skips the multicast Bonjour test
go test ./internal/device -run TestHandshake         # one test
node --test internal/web/static/lighting_test.mjs    # the lighting tests alone
```

- The device tests use a pty pair as the serial port.
- `go test ./internal/remote` runs the remote listener in-process against a fake herdr writer. `go test ./internal/web` does the same for the browser pad.
- `testdata/snapshots/` holds captured `session.snapshot` responses with every private string replaced by a `SECRET-` marker; the decoder test fails if one survives. Capture new fixtures the same way.

Handy commands while working:

```bash
tally status                         # herdr's agents in slot order
tally status --json | tally frame -  # light the pad from live herdr state, once
tally act submit --dry-run           # print an action's herdr calls without making them
tally serve --verbose                # log every frame and action
```

### Trying a live listener without touching your setup

Point `HOME` at a scratch directory so your real pairing, password and certificate stay untouched, with `HERDR_SOCKET_PATH` set to the real socket (use a literal path, not one built from `$HOME`):

```bash
export HERDR_SOCKET_PATH=~/.config/herdr/herdr.sock
HOME=$scratch tally pair --json
HOME=$scratch tally serve --remote --no-device --listen 127.0.0.1:47999
HOME=$scratch tally web --set-password --password-stdin <<< 'a scratch password'
HOME=$scratch tally serve --web --web-cert self --no-device --web-listen 127.0.0.1:47899
```

**Never send keys to live herdr panes** from tests or while developing: real agents run there. Test writes against the fake writer, `--dry-run`, or a scratch tab you create and close.

### Installing your build

```bash
cp ~/.local/bin/tally ~/.local/bin/tally.prev        # keep a way back
go build -o ~/.local/bin/tally ./cmd/tally
launchctl kickstart -k gui/$(id -u)/local.tally      # Linux: systemctl --user restart tally
tally doctor
```

## Rules that hold across changes

- The serial path stays read-only. Only `tally serve --remote`/`--web` and `tally act` construct the herdr writer.
- Clients send action names, never keys. Actions are never retried. Nothing is accepted from a phone before auth, or from a browser before sign-in.
- No agent titles, cwds or terminal output leave the host or reach a log line.
- The v2 serial frame stays byte-compatible with the pad firmware in [vibe-macropad](https://github.com/sroberts/vibe-macropad). Protocol changes for remote clients go in v3 only, and change every client with it.
- Lighting rules match the hardware pad's `render.py`; change them in every client and its tests together.
- No secrets in output you share: don't paste sign-in codes, passwords or tokens into logs, issues or PRs.

## Design docs

- [`design/tally-macropad.md`](design/tally-macropad.md): the daemon, slot ordering, serial protocol v2.
- [`design/remote.md`](design/remote.md): the remote listener for paired phones, pairing and the action table. [`protocol-v3.md`](protocol-v3.md) is its wire format.
- [`design/browser-pad.md`](design/browser-pad.md): the browser pad, its sign-in and certificates.
- [`m0-herdr-input.md`](m0-herdr-input.md): which herdr methods and key names the action table uses.
