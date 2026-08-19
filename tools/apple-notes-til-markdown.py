#!/usr/bin/env python3
"""Apple Noter → Markdown, med formateringen i behold.

Den første udgave af det her brugte `textutil -convert txt`, som er hurtigt at
skrive og smider alt væk: overskrifter, fed, monospace, lister. Ti prøvenoter
viste hvad det koster — en note er ikke kun sine ord, den er også hvordan de er
sat op, og et referat med overskrifter er ikke det samme referat som en klump.

Apple Noter er HTML indeni, og HTML kan læses. Kortet er lille, fordi Noter selv
bruger et lille sæt:

    <h1> <h2> <h3>   overskrifter        →  # ## ###
    <b> <i> <u>      fremhævning         →  ** * <u>
    <tt>             monospace           →  ```-blok
    <ul><li>         punktliste          →  -
    <ol><li>         nummereret          →  1.
    <div>            en linje            →  linjeskift
    <br>             en tom linje        →  tom linje

Kører på din egen maskine og sender ingenting nogen steder.

    ./apple-notes-til-markdown.py ~/Desktop/noter          # alle
    ./apple-notes-til-markdown.py ~/Desktop/noter 10       # de første ti
"""

import html
import os
import re
import subprocess
import sys
from html.parser import HTMLParser

BLOCK_PREFIX = {"h1": "# ", "h2": "## ", "h3": "### ", "h4": "#### "}
FORBIDDEN = set('/\\:*?"<>|')


def osascript(script: str) -> str:
    """Ét kald til Noter. Fejl er tomme frem for fatale: en enkelt note, der ikke
    kan læses, skal ikke stoppe en kørsel på tolv hundrede."""
    try:
        out = subprocess.run(
            ["osascript", "-e", script],
            capture_output=True, text=True, timeout=60, check=False,
        )
        return out.stdout.rstrip("\n")
    except subprocess.SubprocessError:
        return ""


class NoteParser(HTMLParser):
    """Apple Noters HTML til Markdown.

    Linjebaseret, fordi Noter er linjebaseret: hver <div> er en linje, og det er
    dét, personen skrev. En rigtig Markdown-gengiver ville slå linjer sammen til
    afsnit, hvilket er korrekt Markdown og forkert i forhold til noten.
    """

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.lines: list[str] = []
        self.line: list[str] = []
        self.block: str | None = None     # h1, h2 …
        self.mono = False                 # den aktuelle linje er monospace
        self.mono_lines: list[str] = []   # samlet op til én kodeblok
        self.list_kind: list[str] = []    # ul/ol i lag
        self.list_no: list[int] = []
        self.marks: list[str] = []        # b, i, u i lag

    # --- linjer ---------------------------------------------------------------

    def _flush_line(self):
        text = "".join(self.line).rstrip()
        self.line = []
        mono, self.mono = self.mono, False

        if mono:
            # Monospace samles op og skrives som én blok, ikke som en række
            # enkeltlinjer: en terminaludskrift er én ting.
            self.mono_lines.append(text)
            return

        # En tom linje lukker ikke en kodeblok. Hver linje i Noter er pakket ind i
        # sin egen <div>, og dens starttag tømmer den forrige linje — så mellem to
        # <tt>-linjer kommer der altid en tom flush forbi. Lod man den lukke
        # blokken, blev hver eneste linje i en terminaludskrift sin egen blok.
        if not text and self.mono_lines:
            return

        self._close_mono()

        if self.block:
            self.lines.append(BLOCK_PREFIX[self.block] + text if text else "")
            return
        if self.list_kind:
            if not text:
                return
            if self.list_kind[-1] == "ol":
                self.list_no[-1] += 1
                self.lines.append(f"{self.list_no[-1]}. {text}")
            else:
                self.lines.append(f"- {text}")
            return
        self.lines.append(text)

    def _close_mono(self):
        if not self.mono_lines:
            return
        # Tomme linjer i enderne er afstand omkring blokken, ikke en del af koden.
        block = self.mono_lines
        while block and not block[0].strip():
            block.pop(0)
        while block and not block[-1].strip():
            block.pop()
        if block:
            self.lines.append("```")
            self.lines.extend(block)
            self.lines.append("```")
            # Luft efter blokken. Uden den klæber den næste sætning til den
            # afsluttende hegnslinje, og en Markdown-læser er i sin gode ret til
            # at læse dem som ét.
            self.lines.append("")
        self.mono_lines = []

    # --- tags -----------------------------------------------------------------

    def handle_starttag(self, tag, attrs):
        if tag in BLOCK_PREFIX:
            self._flush_line()
            self.block = tag
        elif tag in ("ul", "ol"):
            self._flush_line()
            self.list_kind.append(tag)
            self.list_no.append(0)
        elif tag == "li":
            self._flush_line()
        elif tag in ("tt", "code", "pre"):
            # Ikke et linjeskift: <tt> står inde i den <div>, der ER linjen.
            # Mærket sættes på linjen og aflæses, når den skrives ud.
            self.mono = True
        elif tag in ("b", "strong"):
            self.marks.append("**")
            self.line.append("**")
        elif tag in ("i", "em"):
            self.marks.append("*")
            self.line.append("*")
        elif tag == "u":
            self.marks.append("</u>")
            self.line.append("<u>")
        elif tag == "br":
            self._flush_line()
        elif tag == "div":
            self._flush_line()

    def handle_endtag(self, tag):
        if tag in BLOCK_PREFIX:
            self._flush_line()
            self.block = None
        elif tag in ("ul", "ol"):
            self._flush_line()
            if self.list_kind:
                self.list_kind.pop()
                self.list_no.pop()
        elif tag == "li":
            self._flush_line()
        elif tag in ("tt", "code", "pre"):
            # Blokken lukkes IKKE her. To <tt>-linjer efter hinanden er én
            # terminaludskrift, ikke to; den lukkes, når en linje uden monospace
            # skrives ud, hvilket _flush_line sørger for.
            pass
        elif tag in ("b", "strong", "i", "em", "u"):
            if self.marks:
                self.line.append(self.marks.pop())
        elif tag == "div":
            self._flush_line()

    def handle_data(self, data):
        self.line.append(data)

    def result(self) -> str:
        self._flush_line()
        self._close_mono()
        # Tomme linjer i stimer bliver til én: Noter er fuld af <div><br></div>,
        # og fem tomme linjer i træk er ikke afstand, det er rod.
        out, blanks = [], 0
        for line in self.lines:
            if line.strip():
                blanks = 0
                out.append(line)
            else:
                blanks += 1
                if blanks == 1:
                    out.append("")
        while out and not out[-1].strip():
            out.pop()
        return "\n".join(out)


def to_markdown(body: str) -> str:
    p = NoteParser()
    p.feed(body)
    p.close()
    return p.result()


def safe_name(title: str, fallback: str) -> str:
    name = "".join("-" if c in FORBIDDEN or ord(c) < 32 else c for c in title).strip()
    name = name.rstrip(" .")
    return name[:80] or fallback


def main() -> int:
    out_dir = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/Desktop/apple-noter")
    limit = int(sys.argv[2]) if len(sys.argv) > 2 else 0
    os.makedirs(out_dir, exist_ok=True)

    # Id'erne først, i ét kald.
    #
    # Ikke "note 1", "note 2" …: Noter sorterer listen efter ændringsdato, så et
    # indeks peger på noget andet, hvis noget flytter sig imellem to kald — og
    # navn og krop blev hentet hver for sig. Resultatet var en note med én titel
    # og en andens indhold, hvilket først ses, når man læser dem igennem. Et id
    # peger på den samme note hver gang.
    ids = osascript(
        'tell application "Notes" to return id of every note'
    )
    if not ids:
        print("Noter svarer ikke. Første gang skal macOS have lov — se efter dialogen.")
        return 1
    ids = [x.strip() for x in ids.split(",") if x.strip()]

    total = len(ids)
    print(f"Fandt {total} noter.")
    if limit and limit < total:
        ids = ids[:limit]
        total = limit
        print(f"Tager de første {total}.")

    used: dict[str, int] = {}
    attachments_dir = os.path.join(out_dir, "vedhaeftninger")

    for i, note_id in enumerate(ids, start=1):
        # Navn og krop i ét kald, adskilt af noget, der ikke står i en note.
        # Hentet hver for sig kunne de nå at komme fra hver sin note.
        both = osascript(
            f'tell application "Notes" to tell note id "{note_id}" '
            f'to return name & "\u241E" & body'
        )
        title, _, body = both.partition("\u241E")
        title = title.strip() or f"uden titel {i}"
        text = to_markdown(body)

        # Kroppen begynder med notens egen titel. Skrives der en overskrift
        # ovenover, står titlen to gange i hver eneste note.
        #
        # De tomme linjer skal forbi først: <h1> ligger inde i en <div>, hvis
        # starttag tømmer linjen før den — så første linje er tom, og
        # sammenligningen så på ingenting.
        # Titlen kan være delt over flere overskrifter.
        #
        # Noter kalder noten det, der står på de første linjer, sat sammen med
        # mellemrum — så en note, hvis to første linjer er "CPU" og "CAN", hedder
        # "CPU CAN". Sammenlignede man kun med den første linje, gik det fri, og
        # titlen stod bagefter to gange: én gang som overskrift og én gang som de
        # stumper, den var lavet af. Fjorten noter ud af tolv hundrede.
        #
        # Overskrifterne øverst tages derfor med, så længe de tilsammen stadig er
        # begyndelsen på titlen, og fjernes kun, hvis de til sidst er hele titlen.
        lines = text.split("\n")
        while lines and not lines[0].strip():
            lines.pop(0)

        taken, joined = 0, ""
        for line in lines:
            if not line.strip():
                taken += 1
                continue
            if not line.lstrip().startswith("#"):
                break
            candidate = (joined + " " + line.lstrip("# ").strip()).strip()
            if not title.strip().startswith(candidate):
                break
            joined, taken = candidate, taken + 1
            if joined == title.strip():
                break

        if joined == title.strip():
            lines = lines[taken:]
            while lines and not lines[0].strip():
                lines.pop(0)
        text = "\n".join(lines)

        name = safe_name(title, f"note-{i}")
        used[name] = used.get(name, 0) + 1
        path = os.path.join(out_dir, name if used[name] == 1 else f"{name} ({used[name]})") + ".md"

        with open(path, "w", encoding="utf-8") as f:
            f.write(f"# {title}\n\n{text}\n")

        # Billeder og bilag. En note fra Noter er ofte fuld af fotografier, og en
        # note, der kommer over som ord alene, er ikke den note.
        count = osascript(
            f'tell application "Notes" to return count of attachments of note id "{note_id}"'
        )
        if count.isdigit() and int(count) > 0:
            os.makedirs(attachments_dir, exist_ok=True)
            with open(path, "a", encoding="utf-8") as f:
                for j in range(1, int(count) + 1):
                    a_name = osascript(
                        f'tell application "Notes" to return name of attachment {j} '
                        f'of note id "{note_id}"'
                    ) or f"bilag-{i}-{j}"
                    a_file = f"{i}-{j}-" + safe_name(a_name, f"bilag-{i}-{j}")
                    target = os.path.join(attachments_dir, a_file)
                    # Nogle vedhæftninger — links til websider, kort — har ingen
                    # fil bag sig og fejler her. De springes over.
                    osascript(
                        f'tell application "Notes" to save attachment {j} '
                        f'of note id "{note_id}" in POSIX file "{target}"'
                    )
                    if os.path.exists(target):
                        f.write(f"\n![](vedhaeftninger/{os.path.basename(target)})\n")

        if i % 25 == 0:
            print(f"  {i} / {total}")

    print(f"\nFærdig. {total} noter ligger i:\n  {out_dir}\n")
    print("Pak dem og importér zip'en under Indstillinger → Data:")
    print(f'  cd "{os.path.dirname(out_dir)}" && zip -r "{os.path.basename(out_dir)}.zip" "{os.path.basename(out_dir)}"')
    return 0


if __name__ == "__main__":
    sys.exit(main())
