# Notifications

What other people did that you need to know about, and what the day has in it.
Verdande tells you in three places at once, and they are the same thing said three
ways: the **bell** in the header, a **push** to your devices, and the **badge** on
the app's icon.

## What causes one

| | |
| --- | --- |
| Somebody edited a note shared with you | Repeat edits fold into one line while it is unread |
| Somebody shared a note with you | Once, when it happens |
| Somebody named you in a note with `@` | Instead of "edited a note" — one act, one message |
| Somebody gave you a task | Only on the change, and only to the person who got it |
| Somebody commented | |
| [Today's plan](ai.md), if you turned it on | In the morning, at the hour you chose — it opens the card on **I dag** |
| A new version is out | Administrators only, once per version |

Not your own actions: the server does not tell anybody what they just did
themselves. And not everything that happens — a bell that rings at every change in
a shared project is a bell people stop looking at.

**Repeat edits are folded.** A note is saved on every typing pause, so one person
writing for five minutes would otherwise be twenty identical lines. While the
message is unread, a further edit moves it back to the top and says who touched it
last. Once you have read it, the next edit is a new thing to be told.

## Push, and the badge

**Settings → Beskeder** turns on Web Push for the device you are sitting at, per
device: the phone in your pocket and the laptop you have open are different
answers, and a permission granted on one is not the other's.

The **badge** — the number on the icon in the Dock on a Mac, on the taskbar on a
PC — needs the app to be installed (added to the Dock, or installed as an app in
Chrome or Edge). Half the point of being told is being told while you are doing
something else, and the icon is the only place the app can say anything then. It
counts unread messages and goes down by itself as you read them. Firefox does not
support it; nothing else changes there.

The sentence you see is written in your browser from *what happened* and *who did
it*, so an English interface gets an English bell. The server's own wording is
Danish and is used only for push, which has no dictionary to look a phrase up in.

## Telling you about a new version

If the [update check](configuration.md) is on, administrators get one notification
per version — never one a day about the same one — and it leads to
**Settings → Beskeder**, where the button that restarts the instance to update it
sits. With the check off, nothing is fetched and nothing is said.
