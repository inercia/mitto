# Mitto

Mitto is a command-line interface for interacting with [Agent Communication Protocol (ACP)](https://agentcommunicationprotocol.dev/) servers. It allows you to communicate with AI coding agents like auggie, claude-code, and others that implement ACP.

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/inercia/mitto.git
cd mitto

# Build
make build

# Or install to your GOPATH/bin
make install
```

### Dependencies

Mitto requires Go 1.23 or later.

## Configuration

Mitto uses a YAML configuration file to define ACP servers. 

### Configuration File Location

- **macOS/Linux**: `~/.mittorc`
- **Windows**: `%APPDATA%\.mittorc`

You can override the location using the `MITTORC` environment variable:

```bash
export MITTORC=/path/to/custom/config.yaml
```

### Configuration Format

```yaml
acp:
  - auggie:
      command: auggie --acp
  - claude-code:
      command: npx -y @zed-industries/claude-code-acp@latest
```

The first server in the list is the default. Each server entry is a map with:
- **name** (key): A unique identifier for the server
- **command**: The shell command to start the ACP server

## Usage

### Interactive CLI

Start an interactive session with the default ACP server:

```bash
mitto cli
```

Use a specific ACP server:

```bash
mitto cli --acp claude-code
```

Auto-approve permission requests (use with caution):

```bash
mitto cli --auto-approve
```

### CLI Commands

Once in the interactive CLI, you can use these commands:

| Command | Description |
|---------|-------------|
| `/quit`, `/exit`, `/q` | Exit the CLI |
| `/cancel` | Cancel the current operation |
| `/help`, `/h`, `/?` | Show available commands |

### Global Flags

| Flag | Description |
|------|-------------|
| `--acp <name>` | Select which ACP server to use |
| `--config <path>` | Use a custom configuration file |
| `--auto-approve` | Automatically approve all permission requests |
| `--debug` | Enable debug logging |

## Adding New ACP Servers

To add a new ACP server, edit your `~/.mittorc` file and add a new entry:

```yaml
acp:
  - auggie:
      command: auggie -acp
  - my-new-agent:
      command: /path/to/my-agent --acp-mode
```

The command should start an ACP-compatible server that communicates via stdin/stdout.

## Development

```bash
# Run tests
make test

# Format code
make fmt

# Build and run
make run ARGS="cli"
```

## License

MIT

