# Signing in

Three ways in, and you can have all of them at once.

## A password

What the first account is created with. Nothing is emailed, nothing is registered
anywhere: the first person to open a fresh instance creates the administrator
account, and after that people arrive by invite link.

## Being let into a project

Two different situations, one panel — **the project's ⋯ menu → Share**.

Somebody who **already has an account here** you pick from a list by name. You do
not need to know their email address to share a project with a colleague whose
name you know. The accounts on an instance are a closed set — there is no open
signup, every one of them was invited — so listing them to each other is the
address book the feature needs, not a disclosure.

Somebody who is **not here yet** you invite by email. If the address turns out to
belong to an account it becomes an ordinary share straight away; otherwise it
becomes a link that creates their account and their membership at once. With no
mail server configured the link is shown in the panel instead of being sent, which
is the correct behaviour on a one-person instance rather than an error.

An invitation that has not been taken up stays listed under the members, greyed,
with a × to withdraw it. Without that it is invisible — the person has no account,
so they are neither a member nor somebody you can pick — and the usual next move
is to send it a second time. The second link works exactly as well as the first,
and then there are two to keep track of.

## A code on top of it

**Settings → Account → Two-factor.** An ordinary TOTP secret — any authenticator
app reads the QR code. Once it is on, the password alone is not enough.

Recovery codes are shown once, when you turn it on. They are the way back if the
phone is gone, and they are not shown again.

## A passkey

**Settings → Account → Passkeys → Add a passkey.**

A key on a device instead of a secret in your head. The private half never leaves
the device — a phone's secure element, a laptop's keychain, a hardware key — and
the signature is bound to the address it was made for, so a convincing copy of the
sign-in page at a different address gets nothing.

Give each one a name you will recognise later. *min bærbare* is what makes a list
of keys reviewable, and a list nobody can read is a list nobody revokes from.

Passkeys need the address in `VERDANDE_BASE_URL` to be a hostname. Browsers refuse
to make one for a bare IP address, so an instance reached at `http://192.168.1.10:8080`
will show the passkey section as unavailable rather than failing at the moment
somebody tries.

!!! note "One is enough, two is better"
    Lose the only device holding your only passkey and you are back to the
    password. Keep the password, or register a second key — a phone as well as a
    laptop.
