# Mailboxes

Star a mail and it becomes a task.

This is the other direction from [mail to task](mail.md): instead of forwarding
something, Verdande reads a mailbox you connect and makes a task out of anything
you have flagged. Nothing else in the mailbox is touched, and nothing is ever
sent.

Each mailbox belongs to the person who connected it. Two people on one instance
connect their own and see only their own — there is no shared inbox and no
administrator who can read yours.

## Gmail

**Settings → Integrations → Gmail → Connect.**

Gmail needs an OAuth client, which is the instance's registration with Google
rather than anybody's account — see [Configuration](configuration.md#google).
Once it is set up, each person connects their own mailbox through it.

Choose what makes a task:

| Trigger | What it reads |
|---|---|
| Starred | Anything you have starred |
| Label | Anything carrying a label you name |

!!! note "The app stays Internal if your client is Internal"
    An OAuth client registered as *Internal* in a Google Workspace can only be
    used by accounts in that workspace. A personal `@gmail.com` address will be
    refused with `org_internal`, and that is the client working as configured.
    Switching to *External* means refresh tokens that expire every seven days
    until Google has verified the app — for one personal account, forwarding to
    your [mail-to-task address](mail.md) is the shorter road.

## iCloud, Fastmail and anything else over IMAP

**Settings → Integrations → Mailboxes → Add a mailbox.**

You need three things:

| Field | Example |
|---|---|
| IMAP server | `imap.mail.me.com:993` |
| Username | your address |
| App password | not your ordinary password — see below |

The connection is tested before it is saved. A mailbox that cannot be read is
refused while you are still looking at the form, rather than accepted and then
failing quietly every ten minutes.

Flag a message — the flag, not unread — and it becomes a task on the next sweep.
Unread is a state you are still using yourself, and a sync that consumed it would
be taking something away.

### App passwords

Every host worth naming issues a separate password for applications, which you
can withdraw on its own without changing the one you sign in with. Use that.

- **iCloud** — [appleid.apple.com](https://appleid.apple.com) → Sign-In and
  Security → App-Specific Passwords
- **Fastmail** — Settings → Privacy & Security → Integrations → New app password

The password is [encrypted at rest](configuration.md#secrets), and it is never
returned by the API once saved.

### Office 365 does not work here

Microsoft has closed IMAP to passwords; it wants its own OAuth flow. There is no
app password that will get you in, and a mailbox pointed at `outlook.office365.com`
will be refused. Forward to your [mail-to-task address](mail.md) instead.

## How often

Every ten minutes, in the background. **Fetch now** does it immediately, which is
for when you have just connected something and want to see it work.

A run reads at most twenty-five messages, so a mailbox with years of flagged mail
in it fills up over several sweeps rather than in one.

## Disconnecting

**Disconnect** on the mailbox. The tasks it made stay — they are your work now,
not the mailbox's. Nothing is read after that, and the stored credential is
deleted rather than merely disabled.
