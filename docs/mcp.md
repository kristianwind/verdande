# Claude (MCP)

verdande has a built-in [Model Context Protocol](https://modelcontextprotocol.io)
server, so you can add it to Claude as a connector and work your task list in
conversation.

> *"What's on my plate this week?"*
>
> *"Add a task to call the accountant on Friday, priority 1, in Firma."*
>
> *"What did I not get to that was due last week?"*

## Connecting it

**Add the connector** in Claude with your instance's address followed by `/mcp`,
and nothing else:

```
https://todo.example.dk/mcp
```

Claude sends you to verdande to sign in — the ordinary sign-in, passkey and
second factor included — and then asks one question: *"Claude wants to connect
to your account"*. Press **Allow** and you are connected. Leave the dialog's
OAuth client fields empty; Claude registers itself.

The approval screen shows two things: the name the program gave itself, and
**where the access is sent**. Any program can call itself Claude; only Claude is
answered at `claude.ai`. If that line says something you do not recognise, press
**Deny**.

!!! warning "It has to be reachable from the internet"
    Claude connects from Anthropic's side, not from your browser. An instance only
    reachable on your LAN or over a VPN cannot be used this way.

### With a token in the address instead

A client that cannot sign in can be given a token in the address. **Make a token**
under **Settings → API tokens**; the page then shows the address to go with it:

```
https://todo.example.dk/mcp?key=vrd_…
```

Treat that address like a password: anyone holding it reaches what you reach. The
server logs request paths without their query strings, so it does not end up in
the log.

### With a header instead

Anything that *can* send a header should:

```bash
curl -H "Authorization: Bearer vrd_…" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  https://todo.example.dk/mcp
```

`/api/v1/mcp` answers the same way. Both run the same server and reach the same
things.

### How the sign-in works

verdande is its own OAuth authorization server, for MCP clients only — the shape
the MCP specification asks for:

- `/.well-known/oauth-protected-resource/mcp` and
  `/.well-known/oauth-authorization-server` say where to sign in. An unsigned
  request to `/mcp` is answered 401 with a `WWW-Authenticate` header pointing at
  the first.
- Clients register themselves at `/oauth/register`. Public clients only: a program
  running somewhere else cannot keep a secret, so PKCE (S256) is the proof
  instead. A registration nobody approves is removed after a day.
- The code a client gets back is good for one minute and one exchange.
- What it is exchanged for is an ordinary personal API token. No refresh token and
  no expiry: it is listed under **Settings → API tokens** with the client's name,
  marked *connected with your account*, and connecting the same client again
  replaces it rather than adding another.

## What Claude can do

| Tool | What it does |
|---|---|
| `list_projects` | List your projects. |
| `search_tasks` | Find tasks — by text, or with the [filter language](filters.md). |
| `create_task` | Create one, from natural language or explicit fields. |
| `update_task` | Change a title, priority, date or project. |
| `complete_task` | Tick one off. |
| `add_comment` | Comment on a task. |
| `search_notes` | Find notes by text. |
| `create_note` | Write a note, as Markdown. |
| `update_note` | Change a note. |

`create_task` prefers natural language, so *"add file the VAT return tomorrow at
10am p1 #Accounts"* goes through the same parser the app uses — dates, priorities,
projects, labels and recurrence in either language.

## What it can reach

**Exactly what you can reach, and nothing more.** A token is a person, and a
person's permissions are already decided: shared projects you are a member of, yes;
somebody else's private projects, no. There is no separate service identity and no
scope to configure.

## What it cannot do

Delete projects, manage members, change settings or read another account. The tools
are the nine above; there is no general escape hatch.

!!! note "Revoking it"
    **Settings → API tokens**, delete the token — a connection made by signing in
    is listed there under the program's name. The connector stops working
    immediately, and asks you to sign in again the next time it is used. Nothing
    else about your account is affected.
