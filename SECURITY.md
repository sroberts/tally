# Security

tally lets a phone or browser act on your coding agents, and text typed into an agent can be one approval away from running code. Security reports are welcome and taken seriously.

## Reporting

Please report vulnerabilities privately through GitHub's [private vulnerability reporting](https://github.com/sroberts/tally/security/advisories/new), not in a public issue. Include the version (`tally version`), your OS, and steps to reproduce. You should hear back within a week.

## What tally protects

- **The browser pad** always requires a password (stored as an argon2id hash), serves only over HTTPS, uses `Secure`, `HttpOnly`, `SameSite=Strict` cookies, rate-limits sign-in, and checks the origin of every state-changing request. Sign-in codes are single-use and short-lived.
- **Paired phones** pin tally's certificate from a QR code and authenticate with a revocable token.
- **Actions** reach only agent panes in herdr, through a fixed allowlist of herdr methods. Clients send action names, never raw keys.
- **Privacy:** agent titles, working directories and terminal output never leave the machine or reach a log line.

The full design, including what an attacker on the same network can and can't do, is in [`docs/design/browser-pad.md`](docs/design/browser-pad.md) and [`docs/design/remote.md`](docs/design/remote.md).
