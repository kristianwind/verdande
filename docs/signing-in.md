# Signing in

Three ways in, and you can have all of them at once.

## A password

What the first account is created with. Nothing is emailed, nothing is registered
anywhere: the first person to open a fresh instance creates the administrator
account, and after that people arrive by invite link.

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
