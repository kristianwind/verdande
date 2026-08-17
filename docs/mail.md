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
