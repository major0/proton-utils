# Lumo Commands

The `proton lumo` command group provides access to Proton's Lumo AI
assistant.

## Prerequisites

**Lumo requires cookie-based authentication.** Bearer token sessions
(the default login mode) do not have the scopes needed for the Lumo
API. You must log in with the `--cookie-session` flag:

```sh
proton account login -u <username> --cookie-session
```

If you attempt to use Lumo commands with a Bearer session, you will see:

```
lumo requires cookie-based authentication; current session uses
Bearer auth (re-login with 'proton account login --cookie-session')
```

### Why cookie auth?

Proton's Lumo service uses a separate authentication domain
(`lumo.proton.me`) that requires cookie-scoped session tokens. The
session fork protocol grants service-specific scopes via cookies that
Bearer tokens cannot carry. This matches how the Proton web client
authenticates to Lumo.

## Chat

Interactive conversations with the Lumo AI assistant.

### Create a conversation

```sh
proton lumo chat create <title>
```

Creates a new conversation and enters interactive mode. Each
conversation gets its own space.

### Resume a conversation

```sh
proton lumo chat resume <conversation-id>
```

Loads message history and continues an existing conversation.

### List conversations

```sh
proton lumo chat list [options]
```

Options:
- `-A` / `--all` — include all conversations
- `--project` — show project conversations only
- `--space <id>` — filter by space ID

### Delete a conversation

```sh
proton lumo chat delete <conversation-id>
```

Deletes the conversation and its parent space if the space is empty.

### View conversation history

```sh
proton lumo chat log <conversation-id>
```

Prints the full chat log of a conversation with formatted output.

Options:
- `--color` — color output: always, auto, or never
- `--no-pager` — disable automatic paging
- `--format` — output format (e.g., `json`)

### Copy a conversation

```sh
proton lumo chat cp <source> [destination]
```

Copies a conversation to a new or existing space.

### Interactive mode

Once in a chat session, the following slash commands are available:

```
/help                  Show available commands
/websearch enable      Enable web search for this session
/websearch disable     Disable web search
/exit                  Exit the chat
```

Ctrl+C during generation cancels only the current request — the
session continues.

## Spaces

Spaces are containers for conversations. Simple chats get one space
per conversation; project spaces can hold multiple conversations and
assets.

### List spaces

```sh
proton lumo space list [options]
```

Options:
- `-A` / `--all` — show all spaces (simple + project + deleted)
- `--simple` — show simple chat spaces only
- `--is-empty` — find and list empty spaces

Default: shows project spaces only.

### Create a space

```sh
proton lumo space create <name> [--project]
```

Creates a new space. Use `--project` for a project space (supports
instructions, icons, and linked Drive folders).

### Delete a space

```sh
proton lumo space delete <space-id> [<space-id> ...] [-f]
```

Deletes one or more spaces. Refuses to delete non-empty spaces unless
`-f` / `--force` is set.

### Configure a space

```sh
proton lumo space config <space-id> [options]
```

Without flags, displays the current configuration. With flags, updates:

- `--name <name>` — set the space name
- `--instructions <text>` — set project instructions
- `--icon <icon>` — set the space icon

## Serve (OpenAI-compatible server)

Starts a local HTTP server that exposes Lumo via the OpenAI chat
completions API. Compatible with any tool that speaks the OpenAI
protocol (Cursor, Continue, aider, etc.).

```sh
proton lumo serve [options]
```

Options:
- `--addr <host:port>` — listen address (default: `127.0.0.1:8443`)
- `--api-key <key>` — use a specific API key (not persisted)
- `--new-api-key` — generate and persist a new API key
- `--tls-cert <path>` — custom TLS certificate
- `--tls-key <path>` — custom TLS key
- `--no-tls` — disable TLS, serve plain HTTP

If no `--api-key` is provided, one is automatically generated, persisted
to the config directory, and printed on stderr at startup.

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/chat/completions` | Chat completions (streaming SSE) |
| GET | `/v1/models` | List available models |

### Example: Configure with Cursor

```json
{
  "models": [{"title": "Lumo", "model": "lumo", "provider": "openai"}],
  "openai": {
    "baseUrl": "https://127.0.0.1:8443/v1",
    "apiKey": "<your-api-key>"
  }
}
```

The API key is printed on startup and persisted in the config directory.
