#!/bin/bash
#
# Apple Noter → en mappe med Markdown-filer.
#
# Apple har ingen eksport, der er værd at bruge: "Eksportér som PDF" er et billede
# af en note, ikke noten. Det her læser Notes.app gennem AppleScript, som er den
# eneste understøttede vej ind, og skriver én .md-fil pr. note.
#
# Det kører på din egen maskine og sender ingenting nogen steder. Resultatet er en
# mappe, du kan kigge igennem, før du beslutter dig — og et zip af den mappe er
# præcis det, Verdande importerer.
#
#   ./apple-notes-til-markdown.sh ~/Desktop/noter
#   cd ~/Desktop && zip -r noter.zip noter
#   → Indstillinger → Data → Importér noter
#
# Første gang spørger macOS, om Terminal må styre Noter. Det skal den.

set -euo pipefail

ud="${1:-$HOME/Desktop/apple-noter}"
mkdir -p "$ud"

echo "Læser Noter. Første gang kan det tage et minut, før macOS svarer."

# Én note ad gangen frem for at bygge én kæmpe streng: AppleScript bliver meget
# langsomt på lange strenge, og på et bibliotek af nogen størrelse ser det ud som
# om den er gået i stå.
antal=$(osascript -e 'tell application "Notes" to return count of notes')
echo "Fandt $antal noter."

i=1
while [ "$i" -le "$antal" ]; do
	titel=$(osascript -e "tell application \"Notes\" to return name of note $i" 2>/dev/null || echo "")
	krop=$(osascript -e "tell application \"Notes\" to return body of note $i" 2>/dev/null || echo "")

	if [ -z "$titel" ]; then
		titel="uden titel $i"
	fi

	# Tegn, der ikke kan være i et filnavn på de tre systemer, folk bruger.
	fil=$(printf '%s' "$titel" | tr '/\\:*?"<>|' '-')
	sti="$ud/$fil.md"
	n=2
	while [ -e "$sti" ]; do
		sti="$ud/$fil ($n).md"
		n=$((n + 1))
	done

	# Noter er HTML indeni. textutil følger med macOS og kan lave det om til ren
	# tekst; overskrifter og fed går tabt, men ordene og linjerne overlever, og det
	# er dem, man kommer efter. Titlen sættes som første linje, fordi det er sådan
	# Verdande navngiver en note.
	{
		printf '# %s\n\n' "$titel"
		printf '%s' "$krop" | textutil -stdin -stdout -format html -convert txt 2>/dev/null || true
	} > "$sti"

	# Billeder og bilag.
	#
	# En note fra Apple Noter er ofte fuld af fotografier, scanninger og indsatte
	# skærmbilleder, og en note, der kommer over som ord alene, er ikke den note —
	# den er et referat af den. AppleScript kan gemme hver vedhæftning som en fil;
	# de lægges i en undermappe, og der skrives en henvisning til hver i bunden af
	# noten, så Verdande kan hægte dem på ved importen.
	bilag=$(osascript -e "tell application \"Notes\" to return count of attachments of note $i" 2>/dev/null || echo 0)
	if [ "${bilag:-0}" -gt 0 ]; then
		mkdir -p "$ud/vedhaeftninger"
		j=1
		while [ "$j" -le "$bilag" ]; do
			navn=$(osascript -e "tell application \"Notes\" to return name of attachment $j of note $i" 2>/dev/null || echo "")
			[ -z "$navn" ] && navn="bilag-$i-$j"
			# Samme rensning som titlen: et skråstreg i et filnavn laver en mappe.
			navn=$(printf '%s' "$navn" | tr '/\\:*?"<>|' '-')
			mål="$ud/vedhaeftninger/$i-$j-$navn"

			# `save` skriver filen. Nogle vedhæftninger — links til websider, kort —
			# har ingen fil bag sig og fejler her; de springes over frem for at
			# stoppe hele kørslen.
			if osascript -e "tell application \"Notes\" to save attachment $j of note $i in POSIX file \"$mål\"" >/dev/null 2>&1; then
				printf '\n![](vedhaeftninger/%s)\n' "$(basename "$mål")" >> "$sti"
			fi
			j=$((j + 1))
		done
	fi

	if [ $((i % 25)) -eq 0 ]; then
		echo "  $i / $antal"
	fi
	i=$((i + 1))
done

echo
echo "Færdig. $antal noter ligger i:"
echo "  $ud"
echo
echo "Kig dem igennem, og pak dem så:"
echo "  cd \"$(dirname "$ud")\" && zip -r \"$(basename "$ud").zip\" \"$(basename "$ud")\""
