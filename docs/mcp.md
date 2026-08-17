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

**Make a token** under **Settings → API tokens**. The page then shows the
connector address to go with it — token and all — which is the thing to copy:

```
https://todo.example.dk/mcp?key=vrd_…
```

**Add the connector** in Claude with that address, and nothing else. The dialog
asks for a name and a URL; leave the OAuth fields empty.

!!! note "Why the key is in the address"
    The dialog has no field for a bearer token — only OAuth client credentials —
    so a header cannot be configured from it. The address *is* the credential
    here, exactly as it is for the [calendar feed](caldav.md#subscribing-to-a-feed).
    Treat it like a password: anyone holding it reaches what you reach. The
    server logs request paths without their query strings, so it does not end up
    in the log.

!!! warning "It has to be reachable from the internet"
    Claude connects from Anthropic's side, not from your browser. An instance only
    reachable on your LAN or over a VPN cannot be used this way.

### With a header instead

Anything that *can* send a header should, and use the API path:

```bash
curl -H "Authorization: Bearer vrd_…" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  https://todo.example.dk/api/v1/mcp
```

Both endpoints run the same server and reach the same things.

## What Claude can do

| Tool | What it does |
|---|---|
| `list_projects` | List your projects. |
| `search_tasks` | Find tasks — by text, or with the [filter language](filters.md). |
| `create_task` | Create one, from natural language or explicit fields. |
| `update_task` | Change a title, priority, date or project. |
| `complete_task` | Tick one off. |
| `add_comment` | Comment on a task. |

`create_task` prefers natural language, so *"add betal moms i morgen kl 10 p1
#Firma"* goes through the same parser the app uses — dates, priorities, projects,
labels and recurrence in either language.

## What it can reach

**Exactly what you can reach, and nothing more.** A token is a person, and a
person's permissions are already decided: shared projects you are a member of, yes;
somebody else's private projects, no. There is no separate service identity and no
scope to configure.

## What it cannot do

Delete projects, manage members, change settings or read another account. The tools
are the six above; there is no general escape hatch.

!!! note "Revoking it"
    **Settings → API tokens**, delete the token. The connector stops working
    immediately, and nothing else about your account is affected.
