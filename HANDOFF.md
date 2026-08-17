# verdande — overlevering

Skrevet 17. august 2026. Alt i den oprindelige brief er bygget. Dette dokument er
det, en ny session har brug for at vide, og som ikke kan læses ud af koden.

Læs [README.md](README.md) for hvad projektet er, og [docs/](docs/) for hvordan man
bruger det. Dette er noget andet: beslutningerne, hullerne og fælderne.

---

## Det korte

| | |
|---|---|
| Repo | `kristianwind/verdande`, privat |
| Lokal sti | `~/Documents/Code/ToDoApp` — se [mappenavnet](#mappenavnet) |
| Stack | Go 1.26, SQLite (modernc, ingen cgo), SvelteKit 5, én binary |
| Deployes som | Rune i Yggdrasil Panel; kører også som almindelig Docker |
| CI | Go (fmt, vet, race-tests), Docker-build, MkDocs — alle grønne |
| Omfang | ~21.000 linjer Go, ~3.700 linjer frontend, 557 tests, 17 pakker |

## Navnet

Projektet hed **skuld** i briefen. Det blev omdøbt til **verdande** før første
linje kode.

Grunden er ikke domænerne: **Skuld er en katalogiseret Golang-infostealer-familie**
(Malpedia `win.skuld`, aktiv siden 2023). Det øverste GitHub-resultat for "skuld"
er en stealer skrevet i Go med 477 stjerner. verdande er selv en Go-binary, folk
downloader og selvhoster — sammenfaldet betød AV-falske-positiver og permanent
forgiftede søgeresultater for projektets egen dokumentation.

**Foreslå ikke at gå tilbage.** Hvis et navn nogensinde tages op igen: tjek
GitHubs topresultater og threat intel-kataloger for det præcise ord, før domæner.

---

## Sådan kommer du i gang

```bash
VERDANDE_DATA_DIR=./data VERDANDE_DEV=true go run ./cmd/verdande
```

```bash
cd web && npm install && npm run dev
```

API på 8080, UI på 5173 med `/api` proxyet — så sessionscookien forbliver
first-party præcis som i produktion.

Hele stakken som én binary, sådan som den shipper:

```bash
cd web && npm run build && cd .. && cp -r web/build cmd/verdande/webbuild && go build -tags embedweb -o verdande ./cmd/verdande
```

Frontenden embeddes bag build-taggen `embedweb`, så et rent checkout kompilerer
uden Node installeret.

---

## Beslutninger, der ser mærkelige ud indtil man kender grunden

Alle er kommenteret i koden. Her er de, der har kostet mest at finde ud af.

**Connection-poolen er ikke 1.** Én forbindelse gør poolen til en lås: kode, der
kører et opslag mens et tidligere `*sql.Rows` stadig er åbent, venter på en
forbindelse, den selv holder. Det er en deadlock, ikke en langsom query, og den
viser sig først når en liste har rækker i sig. Store-laget lukker sine resultatsæt
før næste query; poolen efterlader fejlen langsom frem for fatal, hvis den laves
igen.

**Søgning indekserer en genereret `fold`-kolonne ved siden af originalteksten.**
FTS5's `remove_diacritics` rører ikke `ø` og `æ` — Unicode betragter dem som
selvstændige bogstaver, ikke accenterede former. Uden `fold` kan en dansker ikke
finde "grøn" ved at skrive "gron".

**CSP'en udregnes ved opstart** ved at hashe de inline scripts i den byggede
`index.html`. Begge ændrer sig ved hvert frontend-build, så en hardkodet hash ville
blive forældet lydløst og først vise sig som en blank side i produktion.

**Recurrence parses før datoer**, og før klokkeslæt-grenen. "hver mandag"
indeholder en ugedag; en dato-parser der læste først, ville tage mandag som
engangsdato og efterlade "hver" i titlen. Og "hver mandag kl 9" tastet efter kl. 9
ville lande i morgen tidlig i stedet for på mandag.

**Todoists prioriteter er omvendte.** Dens CSV skriver `4` for det, grænsefladen
kalder P1. De konverteres, ikke kopieres — at bytte om ville lydløst invertere
hastegraden på hver eneste importeret opgave.

**Sortering er et flydende tal.** Et drop skriver én række. Når gentagne drop i
samme mellemrum opbruger præcisionen, spredes sektionen ud igen og flytningen
prøves forfra — i en *separat* transaktion, siden det forsøg, der opdagede
problemet, ruller tilbage.

**En gentagen opgave lukkes ikke; den rykker frem i samme række.** Id'et er
stabilt, så undertasks og kommentarer bliver hængende. En completion skrives
stadig til activity-loggen.

**Alt, kalderen ikke må se, svarer 404 — aldrig 403.** En 403 bekræfter, at tingen
findes.

**Vedhæftninger serveres altid som `application/octet-stream`.** En uploadet SVG
vist inline ville køre sit eget script på dette origin med sessionscookien
vedhæftet. Filer gemmes indholdsadresseret efter hash, så stien aldrig indeholder
noget, uploaderen har valgt.

**Backups bruger `VACUUM INTO`, ikke en filkopi**, og roteres efter antal, ikke
alder. En container, der har været slukket en måned, må ikke komme tilbage og
slette alle sine backups for at være for gamle.

---

## Hvad der er testet, og hvad der ikke er

**Testet grundigt:** parseren (dansk+engelsk, inkl. gentagelser), permissions
(viewer kan ikke mutere, ekstern bruger ser kun delte projekter), filter-sproget
(inkl. SQL-injection), RRULE, ICS-formatet, Todoist-roundtrip, backup-integritet,
auth-flowet, MCP-protokollen, CalDAV-verberne.

**Implementeret mod specifikation, men aldrig kørt mod den rigtige tjeneste:**

- **Web Push** — krypteringen er skrevet direkte mod RFC 8291 og er ikke verificeret
  mod en levende push-service. Hvis noget ikke virker, er det her, jeg ville
  begynde.
- **Gmail API-kaldene** — OAuth-flowet er testet (PKCE, state, callback), men der er
  aldrig udvekslet et rigtigt token.
- **AI-adapterne** — ingen nøgle i miljøet. Anthropic-, OpenAI- og Google-formerne
  er skrevet efter deres dokumentation.
- **CalDAV mod en rigtig klient** — verberne er testet mod egne requests, ikke mod
  Apple Reminders eller Thunderbird.
- **SMTP** — falder tilbage til at logge beskeden, når SMTP ikke er sat op, og det
  er den sti, testene rammer.

**Docker-imaget er aldrig bygget lokalt** — Docker kørte ikke på maskinen. Det er
verificeret gennem CI, hvor det bygger med frontenden indeni.

---

## Huller, i den rækkefølge jeg ville tage dem

### 1. Frontenden mangler hele indstillingsfladen

Det er det største hul. Følgende virker gennem API'et, men har **ingen UI**:

- Kommentarer og vedhæftninger på en opgave
- Reminders
- Templates (gem projekt som skabelon, opret fra skabelon)
- Import og eksport
- API-tokens
- Gmail-forbindelsen, AI-indstillinger, kalenderfeed, mail-adresse
- Notifikationer og update-beskeden

Der findes ingen `/indstillinger`-rute overhovedet — Gmail-callbacket redirecter
til den, og den giver 404 i dag. Det ville være det første, jeg lavede.

### 2. Opgavedetalje-visning

`TaskRow` har en `onedit`-prop, som ingen bruger. Der er ingen måde at åbne en
opgave og se dens beskrivelse, undertasks, kommentarer eller vedhæftninger på.

### 3. Drag-and-drop i listevisningen

Board-visningen har det. Listevisningen har ikke — API'et (`POST /tasks/{id}/move`)
er testet og virker.

### 4. Gmail: verificér mod en rigtig konto

Registrér en OAuth-klient, sæt `VERDANDE_GMAIL_CLIENT_ID` og `_SECRET`, og kør
flowet igennem. Den mest sandsynlige fejl er redirect-URI'en, som udledes af
`VERDANDE_BASE_URL` og skal matche det registrerede *præcist*.

### 5. Web Push mod en rigtig browser

Der er ingen service worker i frontenden endnu — `web/static/` har manifest og
ikon, men ingen `sw.js`. Serverdelen er færdig.

Manifestet peger kun på `icon.svg`. Chrome og Firefox installerer fint på det;
Safari vil have en PNG som `apple-touch-icon`, men der er ingen SVG-renderer på
maskinen, så referencerne til PNG-filer blev fjernet frem for at pege på filer,
der ikke findes. Rendér dem, hvis PWA'en skal se rigtig ud på iOS.

### 6. E2E-røgtests med Playwright

Briefen bad om dem under "Definition of done". **De findes ikke.** Der er 557 tests,
men alle er Go: ingen af dem åbner en browser.

Det er ikke akademisk. Begge de fejl, der blev fundet allersidst — etiket-ruten
skrevet uden for routes-træet, og et manifest der lovede ikoner, som aldrig blev
genereret — overlevede netop fordi intet rører frontendens rutetræ. Go-testene
kunne ikke have fanget nogen af dem.

En røgtest på fire flows ville dække det meste: log ind, opret en opgave via quick
add, luk den, og klik hvert link i sidebjælken.

### 7. OpenAPI-specifikationen

Briefen bad om en. `docs/api.md` er skrevet i hånden og er komplet, men der er ingen
maskinlæsbar spec.

---

## Fælder

**`chi` kender ikke WebDAV-metoder.** `PROPFIND` og `REPORT` registreres i en
`init()` i `caldav_handlers.go`. Uden det panikker routeren ved *konstruktion*,
ikke ved request — så hver eneste test fejler med noget, der ikke ligner CalDAV.

**Tests må ikke bruge processens lokale dato.** CI kører i UTC, API'et opløser
datoer i brugerens tidszone (Europe/Copenhagen). I to timer af døgnet er de uenige.
Brug `userDate(t, offset)` i `httpapi`-testene. Suiten er kørt grøn under
`TZ=Pacific/Kiritimati`.

**`gofmt -l .` skal være tom.** CI fejler på det, og det er den nemmeste måde at
spilde en runde på.

**Migrationer er additive.** `internal/store/migrations/` køres i filnavnsorden,
hver i sin egen transaktion. Ret aldrig en migration, der er pushet.

**Genskab screenshots med `go run ./tools/shots`** mod en kørende instans. Chromes
eget `--screenshot` kan ikke sætte en sessionscookie og fotograferer kun
login-siden.

---

## Mappenavnet

Arbejdskopien ligger i `~/Documents/Code/ToDoApp`, som var en pladsholder oprettet
før projektet havde et navn. Alle andre projekter i mappen hedder det, de er.

Omdøbning til `verdande` er sikker: git-remote, modulsti og CI refererer ikke den
lokale sti. Gør det når ingen session har en shell åben i den.

---

## Publicering

Repoet er privat. Alt til at gøre det offentligt ligger klar: MIT-licens,
CONTRIBUTING, SECURITY, issue-skabeloner, dokumentation og landing page.

Pages-deployment ligger bag `workflow_dispatch` — dokumentationen bygges ved hvert
push, men intet publiceres, før du kører workflowet manuelt. Landing pagen i
`site/` er statisk og klar til Cloudflare Pages; den skal bare pege på et domæne.

**verdande.app**, **verdande.dev** og **verdande.io** var alle ledige, da navnet
blev valgt. De er ikke købt.

---

## Hvor denne session slap

Sidste commit: `fix(web)` — etiket-ruten og PWA-ikonerne. CI grøn på Go, Docker og
dokumentation.

Alt i den oprindelige brief er bygget, med de undtagelser, der står under
[huller](#huller-i-den-rækkefølge-jeg-ville-tage-dem) — hvoraf frontendens
indstillingsflade og Playwright-røgtestene er de to, jeg selv ville tage først.

Ingen løse ender i arbejdstræet: `git status` er ren, og der ligger ikke halvfærdig
kode nogen steder.
