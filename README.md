# beammp-deploy

A CLI tool that deploys your BeamMP modules to remote servers over SSH. Point it at your config, and it handles the rest — pulling releases from GitHub, syncing local mods, caching what hasn't changed, and uploading everything to the right place.

[![CI](https://github.com/acm-gaming/beammp-deploy/actions/workflows/ci.yml/badge.svg)](https://github.com/acm-gaming/beammp-deploy/actions/workflows/ci.yml)

## Quick Start

1. Grab the latest binary from [Releases](https://github.com/acm-gaming/beammp-deploy/releases) (Linux, macOS, and Windows are all supported).

2. Create a config file at `~/.config/beammp-deploy/config.toml`:

```toml
[[servers]]
name = "my-server"
ssh = "root@192.168.1.1"
path = "/var/lib/server/1"

[[servers.modules]]
name = "my-cool-mod"
repository = "https://github.com/example/my-cool-mod"
```

3. Run it:

```bash
beammp-deploy
```

That's it. You'll see a live progress display as modules get deployed. If something's already up to date, it gets skipped automatically.

## Config

The config file lives at:
- **Linux / macOS:** `~/.config/beammp-deploy/config.toml` (or `$XDG_CONFIG_HOME/beammp-deploy/config.toml`)
- **Windows:** `%AppData%/beammp-deploy/config.toml`

You can also pass `--config /path/to/config.toml` if you want to use a different file.

### Setting up servers

Each server needs a name, an SSH target, and a remote path:

```toml
[[servers]]
name = "production"
ssh = "deploy@10.0.0.5"
path = "/opt/beammp/server1"
# key = "/path/to/id_rsa"  # optional — if your default SSH key isn't the right one
```

### Adding modules

Modules go under the server they belong to. There are a few ways to pull them in:

**From a GitHub repo (using releases):**
```toml
[[servers.modules]]
name = "traffic-mod"
repository = "https://github.com/someone/traffic-mod"
```
This grabs the latest release automatically.

**From a GitHub repo (using a branch):**
```toml
[[servers.modules]]
name = "dev-mod"
repository = "https://github.com/someone/dev-mod"
branch = "main"
```
Use this when the repo doesn't publish releases, or you want to track a specific branch.

**From a local folder (great for development):**
```toml
[[servers.modules]]
name = "my-wip-mod"
local = "/Users/me/projects/my-wip-mod"
```

Any module can also include `path = "some/subdir"` if the actual mod files live in a subdirectory.

### Interactive config editor

Don't want to edit TOML by hand? There's a built-in TUI for that:

```bash
beammp-deploy config
```

It walks you through adding, editing, and removing servers and modules with a menu-driven interface. Arrow keys (or `j`/`k`) to navigate, `Enter` to select, `Esc` to go back.

## Usage

```bash
# Deploy everything in your config
beammp-deploy

# Deploy only specific servers
beammp-deploy --server production --server staging

# See detailed logs
beammp-deploy --verbose

# Disable the TUI (useful for CI or piping output)
beammp-deploy --no-tui
```

## How It Works

When you run a deploy, here's what happens:

1. **Fetches modules** — checks GitHub for the latest release (or pulls the latest commit for branch-based sources), or reads from your local directory.
2. **Checks the cache** — if a module hasn't changed since the last deploy, it gets skipped. No wasted uploads.
3. **Uploads via SSH** — files get synced to the remote server. It automatically figures out where `Server/` and `Client/` files should go based on your mod's directory structure.
4. **Cleans up stale paths** — if a module's target paths changed, old files get removed from the server.

Cache data is stored at `~/.cache/beammp-deploy/cache.json` (or the OS equivalent).

## Private Repos

If you need to pull modules from private GitHub repositories, set one of these environment variables:

```bash
export GITHUB_TOKEN=ghp_xxxxxxxxxxxx
# or
export GITHUB_PAT_TOKEN=ghp_xxxxxxxxxxxx
```

## Development

You'll need [Go](https://go.dev/) and [Task](https://taskfile.dev/) installed.

```bash
task setup    # install dependencies and tools
task build    # build the binary
task test     # run tests
task lint     # run linters
```

To run directly during development:

```bash
task run -- --config ~/.config/beammp-deploy/config.toml
```
