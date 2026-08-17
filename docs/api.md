# API

REST under `/api/v1`, JSON in and out. Everything the web interface does goes
through it, so anything you can do there you can script.

## Authenticating

Two ways, accepted everywhere.

**A session cookie**, which the web interface uses. `httpOnly`, so nothing but the
browser handles it.

**A personal API token**, which is what scripts use:

```bash
curl -H "Authorization: Bearer vrd_..." https://todo.example.dk/api/v1/tasks
```

Make one under **Settings → API tokens**. It carries your permissions exactly —
there is no separate service identity.

## Errors

Every failure has the same shape:

```json
{
  "code": "validation_failed",
  "error": "one or more fields are not valid",
  "fields": { "due_date": "must be a date like 2026-03-15" }
}
```

`code` is stable and meant to be matched on. `error` is English prose for a log —
what a person reads is decided by the client, which knows what language they are in.

| Code | Status | Means |
|---|---|---|
| `bad_request` | 400 | The body could not be read. |
| `unauthorized` | 401 | Not signed in, or the token is wrong. |
| `totp_required` | 401 | The login still needs its verification code. |
| `forbidden` | 403 | Cross-site request, or admin needed. |
| `not_found` | 404 | Does not exist, or you cannot see it. |
| `conflict` | 409 | Already exists, or the state does not allow it. |
| `validation_failed` | 422 | Check `fields`. |
| `rate_limited` | 429 | Too many attempts. |
| `internal_error` | 500 | Our fault. |

!!! note "404 rather than 403"
    Anything you may not see answers 404. A 403 would confirm the thing exists,
    which is what somebody trying ids is hoping to learn.

## The endpoints

### Tasks

| Method | Path | |
|---|---|---|
| `GET` | `/tasks` | List. Filter with `project_id`, `label_id`, `priority`, `due_before`, `due_from`, `no_date`, `assignee_id=me`, `q`, `completed=include\|only`. |
| `POST` | `/tasks` | Create. |
| `POST` | `/tasks/quick-add` | Create from one line of natural language. |
| `GET` | `/tasks/quick-add/preview?text=` | Parse without saving. |
| `GET` | `/tasks/{id}` | One task. |
| `PATCH` | `/tasks/{id}` | Change it. Only the fields you send. |
| `DELETE` | `/tasks/{id}` | To the trash, recoverable for 30 days. |
| `POST` | `/tasks/{id}/complete` | Tick off. A repeating task advances instead. |
| `POST` | `/tasks/{id}/reopen` | Un-tick. |
| `POST` | `/tasks/{id}/move` | Reorder: `{"after_id": …, "before_id": …}`. |

```bash
curl -X POST https://todo.example.dk/api/v1/tasks/quick-add \
  -H "Authorization: Bearer vrd_..." \
  -H "Content-Type: application/json" \
  -d '{"text": "betal moms i morgen kl 10 p1 #Firma @regnskab"}'
```

### Projects and sections

| Method | Path | |
|---|---|---|
| `GET` `POST` | `/projects` | List, create. |
| `GET` `PATCH` `DELETE` | `/projects/{id}` | Read, change, trash. Owner only for the last two. |
| `GET` `POST` | `/projects/{id}/sections` | |
| `PATCH` `DELETE` | `/sections/{id}` | |
| `GET` | `/projects/{id}/members` | |
| `POST` | `/projects/{id}/invites` | Share it. `{"email": …, "role": "editor"\|"viewer"}` |
| `DELETE` | `/projects/{id}/members/{userID}` | |
| `GET` | `/projects/{id}/activity` | |

### Views, filters and labels

| Method | Path | |
|---|---|---|
| `GET` | `/today` | Overdue and due today, in your timezone. |
| `GET` | `/upcoming?days=7` | One entry per day, empty days included. |
| `GET` | `/search?q=` | Across everything you can see. |
| `GET` `POST` | `/filters` | |
| `GET` | `/filters/{id}/tasks` | Run one. |
| `GET` | `/filters/preview?query=` | Run an expression without saving it. |
| `GET` `POST` | `/labels` | |

### Comments and attachments

| Method | Path | |
|---|---|---|
| `GET` `POST` | `/tasks/{id}/comments` | |
| `PATCH` `DELETE` | `/comments/{id}` | Author only for editing. |
| `POST` | `/tasks/{id}/attachments` | `multipart/form-data`, field `file`, 25 MB. |
| `GET` `DELETE` | `/attachments/{id}` | |

### Import and export

| Method | Path | |
|---|---|---|
| `POST` | `/import/todoist` | A Todoist CSV, `multipart/form-data`. |
| `POST` | `/import/csv` | Mapped rows as JSON. |
| `GET` | `/export/account` | Everything, as JSON. |
| `GET` | `/export/projects/{id}.csv` | Todoist-compatible. |
| `GET` | `/export/projects/{id}.ics` | A calendar file. |

### Everything else

| Method | Path | |
|---|---|---|
| `GET` | `/ws` | WebSocket. Live changes to projects you can see. |
| `GET` | `/notifications` | |
| `GET` `POST` | `/feed`, `/feed/rotate` | The [calendar feed](caldav.md) URL. |
| `GET` `POST` | `/mail-address`, `/mail-address/rotate` | The [mail-to-task](mail.md) address. |
| `POST` | `/mcp` | The [MCP](mcp.md) endpoint. |
| `GET` | `/healthz` | Unauthenticated. Reads the database rather than merely answering. |

## Live updates

```javascript
const socket = new WebSocket('wss://todo.example.dk/api/v1/ws');
socket.onmessage = (event) => {
  const { type, project_id, payload } = JSON.parse(event.data);
  // task.created, task.updated, task.completed, task.deleted,
  // task.moved, comment.created, notification, reminder
};
```

One-way by design: nothing a client sends over the socket changes anything. Writes
go through the REST API, where the permission checks are.

## Rate limits

Only where guessing matters: ten login attempts per address per fifteen minutes,
five password-reset requests per hour. The rest of the API is not limited — it is
your server.
