#!/usr/bin/env python3
"""Pakker en Apple Noter-eksport i portioner, der kan importeres.

Eksporten af tolv hundrede noter fylder knap to hundrede megabyte, og næsten
alt sammen er billeder: teksten selv er under fire. Importen tager 64 MB ad
gangen, og tunnelen foran den tager mindre — så det skal deles.

Delt efter hvad en note *vejer med sine billeder*, ikke efter antal. De fleste
noter har ingen, så de fylder én portion til sammen; de få med et fotoalbum i
får hver deres. En note og dens billeder ligger altid i samme portion, for
linkene i teksten er relative, og en note, hvis billeder kom med i næste
portion, ville pege på ingenting.

    ./pak-noter-i-portioner.py ~/Desktop/apple-noter [MB]
"""

import os
import re
import sys
import zipfile

# Under importens 64 MB og under det, en tunnel plejer at tage. Hellere en
# portion mere end en, der ryger på målstregen.
DEFAULT_LIMIT_MB = 40

LINK = re.compile(r"!\[[^\]]*\]\((vedhaeftninger/[^)]+)\)")


def main() -> int:
    src = os.path.expanduser(sys.argv[1] if len(sys.argv) > 1 else "~/Desktop/apple-noter")
    limit = int(float(sys.argv[2] if len(sys.argv) > 2 else DEFAULT_LIMIT_MB) * 1024 * 1024)
    out_dir = src.rstrip("/") + "-portioner"
    os.makedirs(out_dir, exist_ok=True)

    notes = sorted(f for f in os.listdir(src) if f.endswith(".md"))
    if not notes:
        print(f"Ingen .md-filer i {src}")
        return 1

    # Hvad hver note vejer, og hvad den trækker med sig.
    weighed = []
    for name in notes:
        path = os.path.join(src, name)
        text = open(path, encoding="utf-8", errors="replace").read()
        files = []
        size = os.path.getsize(path)
        for rel in LINK.findall(text):
            f = os.path.join(src, rel)
            if os.path.exists(f):
                files.append(rel)
                size += os.path.getsize(f)
        weighed.append((name, files, size))

    # En note, der alene er større end grænsen, får sin egen portion frem for at
    # blive sprunget over: den skal med, og en portion på 25 MB er stadig en, der
    # kan sendes.
    batches, current, current_size = [], [], 0
    for item in weighed:
        if current and current_size + item[2] > limit:
            batches.append(current)
            current, current_size = [], 0
        current.append(item)
        current_size += item[2]
    if current:
        batches.append(current)

    print(f"{len(notes)} noter → {len(batches)} portioner\n")
    for i, batch in enumerate(batches, start=1):
        zip_path = os.path.join(out_dir, f"noter-{i:02d}.zip")
        seen = set()
        with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
            for name, files, _ in batch:
                z.write(os.path.join(src, name), name)
                for rel in files:
                    if rel in seen:
                        continue
                    seen.add(rel)
                    z.write(os.path.join(src, rel), rel)
        mb = os.path.getsize(zip_path) / 1024 / 1024
        print(f"  {os.path.basename(zip_path)}  {len(batch):4d} noter  {len(seen):3d} bilag  {mb:6.1f} MB")

    print(f"\nLigger i:\n  {out_dir}")
    print("\nImportér én ad gangen under Indstillinger → Data → Noter.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
