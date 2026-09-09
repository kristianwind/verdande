# Mail to task

Forward an email and it becomes a task.

## Your address

**Settings → Mail to task** gives you something like:

```
todo+kJ8xN2pQ...@example.dk
```

The token is in the local part rather than the domain, which is what lets a single
mail alias route it without a wildcard domain or a DNS change per person.

## Using it

Forward anything to it. The **subject becomes the task** and the **body becomes the
description**.

The subject goes through the same [quick-add parser](quick-add.md) as everything
else, so this works:

> **Subject:** Send årsregnskab i morgen p1 #Firma

and arrives as a task called *Send årsregnskab*, due tomorrow, priority 1, in
Firma.

## Routing the mail

verdande does not run a mail server. Something has to hand it the message.

=== "Mailcow"

    Make an alias for `todo+*@yourdomain` that pipes to a small script, and have
    the script POST the parsed message to:

    ```
    POST https://todo.example.dk/inbound/mail
    Content-Type: application/json

    {
      "to": "todo+TOKEN@example.dk",
      "from": "afsender@example.dk",
      "subject": "Send årsregnskab i morgen p1 #Firma",
      "body": "Vedhæftet er sidste års tal."
    }
    ```

=== "Anything else"

    The endpoint takes that JSON from anywhere — a Postfix pipe, a Cloudflare Email
    Worker, an IMAP poller. The token in `to` is the whole credential, so no other
    authentication is needed and none is accepted.

## Security

The token *is* the credential — anybody who can send mail to that address can
create tasks in your account. It cannot read anything, and it cannot reach anything
else.

An unknown token and a malformed address answer identically, so the endpoint cannot
be used to work out which addresses are live.

**Settings → Mail to task → Rotate** issues a new address and stops the old one
immediately.

!!! tip "Keep the address to yourself"
    Treat it like a secret. If you publish it somewhere, rotate it.

## Pushing from another program

The address above needs a mail server. When what you want is a shortcut on your
phone, a script, or any service that can call a URL, there is a second way in:
**Settings → Integrationer → Skub fra andre programmer** gives you an address to
POST a line of text to.

```bash
curl -X POST https://todo.example.dk/inbound/hook/<token> \
  -H "Content-Type: text/plain" \
  -d "Ring til Anders i morgen p1 #Firma"
```

The body is read three ways, because those are the three you meet: plain text from
a shortcut that just sends what was selected, JSON with a `text` field from a
service, or a form field from a webhook form. The first line becomes the task and
the rest sits underneath it, and the line goes through the [quick add](quick-add.md)
parser — so "i morgen", "p1" and "#Firma" mean here what they mean in the box at the
top of the app.

**The address is a key.** It has its own token rather than sharing the mail one:
the two are the same kind of secret but they leak separately, and a URL that has
been sitting in a shortcut on a lost phone has to be replaceable without changing
the address other people have in their address books. A token here can do one
thing — put a task in your inbox — which is why it is not an
[API token](api.md), which can do everything. Change it under the same setting; the
old one stops working at once.

**POST only.** A GET would be easier to call, and that is exactly the problem: a
browser prefetch, a link check in a chat, and a revisited history entry would all
create tasks nobody asked for.

