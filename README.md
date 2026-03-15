# beammp-deploy

A CLI package/deployment manager for BeamMP modules.

[![CI](https://github.com/acm-gaming/beammp-deploy/actions/workflows/ci.yml/badge.svg)](https://github.com/acm-gaming/beammp-deploy/actions/workflows/ci.yml)

## Tech Stack

- Go
- Cobra
- Zap
- go-github
- go-git
- pkg/sftp
- TOML v2 parser

## Setup

```bash
task setup
```

## Common Commands

```bash
task run -- --config ~/.config/beammp-deploy/config.toml
task test
task lint
task build
```

## Config

Default config path:
- Linux/macOS: `${XDG_CONFIG_HOME:-~/.config}/beammp-deploy/config.toml`
- Windows: `%AppData%/beammp-deploy/config.toml`

Example:

```toml
[[servers]]
name = "my-server"
ssh = "root@192.168.1.1"
path = "/var/lib/server/1"

[[servers.modules]]
name = "my-mod"
# One of:
repository = "https://github.com/example/my-mod"
# local = "/Users/me/dev/my-mod"
# Optional:
# branch = "main" # required when repository has no releases
# path = "subdir"  # optional sub-path within local or repository source
```

## Usage

Deploy all servers in config:

```bash
beammp-deploy --config ~/.config/beammp-deploy/config.toml
```

Deploy selected servers:

```bash
beammp-deploy --server my-server --server staging-server
```

Verbose logs:

```bash
beammp-deploy --verbose
```

Interactive config management:

```bash
beammp-deploy config
```

`config` runs a full-screen interactive TUI editor with a main menu for add/edit/remove/save operations. Use arrow keys (or `j`/`k`) and `Enter` to navigate, `Esc` to cancel.

Notes:
- Cache is stored in `${XDG_CACHE_HOME}/beammp-deploy/cache.json` (or OS equivalent).
- If `GITHUB_TOKEN` or `GITHUB_PAT_TOKEN` is set, it is used for private repositories.
