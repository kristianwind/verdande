# verdande — overlevering

Skrevet 17. august 2026, opdateret samme dag efter at hullerne blev lukket. Dette
dokument er det, en ny session har brug for at vide, og som ikke kan læses ud af
koden.

Læs [README.md](README.md) for hvad projektet er, og [docs/](docs/) for hvordan man
bruger det. Dette er noget andet: beslutningerne, hullerne og fælderne.

---

## Det korte

| | |
|---|---|
| Repo | `kristianwind/verdande`, privat |
| Lokal sti | `~/Documents/Code/verdande` |
| Stack | Go 1.26, SQLite (modernc, ingen cgo), SvelteKit 5, én binary |
| Udgivet | `v0.3.0` — `ghcr.io/kristianwind/verdande`, amd64 + arm64 |
| Deployes som | Rune i Yggdrasil Panel; kører også som almindelig Docker |
| CI | Go (fmt, vet, race), OpenAPI-lint, Playwright, Docker-build, MkDocs |
| Omfang | ~23.000 linjer Go, ~7.100 linjer frontend, 610 tests + 6 røgtests, 20 pakker |

## GHCR-pakken skal være offentlig

Ikke af princip — fordi Yggdrasil ikke kan andet.

Panelets `PullImage` kalder Docker-SDK'ets `ImagePull` med et tomt
`image.PullOptions{}`. `RegistryAuth` bliver aldrig sat, og der findes ingen
registry-legitimationsoplysninger nogen steder i panelet. Et privat image svarer
altså 401. `pullImageRetry` kasserer den fejl ("second failure surfaces at
create"), og `ContainerCreate` — som kun slår op lokalt — melder bagefter:

```
create container: Error response from daemon:
No such image: ghcr.io/kristianwind/verdande:latest
```

Beskeden lyver: taggen findes udmærket i registret.

**`docker login` på værten hjælper ikke.** Det er rent klient-side — dæmonen
gemmer ingen legitimationsoplysninger, CLI'en sender dem selv pr. request.
Yggdrasil er en anden klient og sender ingen. Det kostede en runde at finde ud af,
så det står her.

Vil man alligevel holde pakken privat, skal imaget lægges på værten manuelt med
`docker login` efterfulgt af `docker pull` — som den bruger, dæmonen kører som —
før serveren oprettes, og igen efter hver udgivelse. Panelets eget pull fejler
stadig lydløst; det finder bare imaget, der allerede er der. Det skjuler
opdateringer: panelet henter ved hver recreate, fejler stille og kører videre på
det gamle lokale image, så en ny version *ser ud* til at rulle ud uden at gøre
det.

**Serveren skal ikke slettes for at prøve igen.** Image-referencen læses fra runen
ved container-create, og både Start og Restart genskaber containeren. Når imaget
kan hentes, er et almindeligt Start nok.

**En rune opdaterer ikke sig selv.** Panelet skriver rune-rækken fra en
UI-handling — hverken genstart eller timer gør det — så en ændring i
`rune/verdande.yaml` kræver **Runes → verdande → Update**. Katalog-listen caches
ti minutter i hukommelsen, så to forsøg lige efter hinanden kan begge give den
gamle version.

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

Røgtestene bygger hele den kæde selv og starter den rigtige binary:

```bash
cd web && npm run test:e2e
```

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

**Transaktioner starter med `BEGIN IMMEDIATE`** (`_txlock=immediate` i DSN'en).
Med almindelig `BEGIN` begynder en skrivetransaktion som læser og beder om
skrivelåsen undervejs — og den opgradering afviser SQLite med `SQLITE_BUSY`
*øjeblikkeligt* uden at bruge `busy_timeout`. Timeouten så altså ud til at dække
samtidige skrivere og dækkede ingen af dem: to mennesker, der gemte samtidig, fik
en 500 efter to millisekunder. `internal/store` har en test, der fejler uden det.

**Listen af opgaver ændres kun gennem `app.replace()` og `app.upsert()`**, som
begge bygger et nyt array. `tasks[i] = ...` nåede ikke frem til visningerne: en
opgave, man lukkede, beholdt sin række, og en ændring over websocket viste sig
slet ikke, mens `push` og hel-array-tildeling begge virkede. Frem for at gætte på
hvilke mutationer reaktiviteten kan se, laver hver skrivning et nyt array.

**Den samme opgave ankommer to gange** — én gang som svar på requesten, der
oprettede den, og én gang over websocket, som udsender til hele projektet
inklusive den, der gjorde det. Derfor `upsert` frem for at tilføje: to rækker med
samme id i et keyed `{#each}` er ikke en dublet, det er en kastet fejl, der
stopper visningen.

**API-tokens kan kun styres med en session.** En bearer-token får 403 på
`/tokens`, selvom den accepteres alle andre steder. Ellers er en lækket token
permanent: tyven udsteder nummer to, og at tilbagekalde den første ændrer intet.

**MCP findes to steder, og det ene tager nøglen i URL'en.** `/api/v1/mcp` vil
have en `Authorization`-header. Claudes connector-dialog spørger om navn, adresse
og OAuth-legitimation — der er intet felt til en bearer-token, så den sti kan slet
ikke sættes op derfra. Derfor `POST /mcp?key=…`, med samme begrundelse som
kalenderfeedet: en klient, man ikke kan bede om at sende en header, betyder at
adressen *er* legitimationen. Loggeren skriver `r.URL.Path` uden query, så nøglen
havner ikke i logfilerne.

Den rute ligger uden for `/api/v1` med vilje og accepterer **kun** nøglen. Læste
den også en sessionscookie, ville den være en tilstandsændrende POST uden for
CSRF-checket — altså en cross-site-request, der handler som den, der er logget
ind. Der er en test, der fejler, hvis det nogensinde bliver sandt.

Fejlen, det kostede at finde: `/mcp` fandtes ikke, så SPA-fallbacken svarede
**200 med app-skallen**. Connectoren meldte forbundet og fejlede så på at læse en
HTML-side — hvilket ligner en i stykker klient frem for en manglende rute.

**En databasefejl er en 500, ikke en 401.** `authenticate()` kan ikke selv se
forskel — begge dele kommer tilbage som en fejl fra sessionsopslaget. Svarer man
401 på begge, viser en diskfejl sig som at alle bliver logget ud på én gang, og
loggen fortæller om en bølge af mislykkede logins i stedet for om fejlen.

---

## Hvad der er testet, og hvad der ikke er

**Testet grundigt:** parseren (dansk+engelsk, inkl. gentagelser), permissions
(viewer kan ikke mutere, ekstern bruger ser kun delte projekter), filter-sproget
(inkl. SQL-injection), RRULE, ICS-formatet, Todoist-roundtrip, backup-integritet,
auth-flowet, MCP-protokollen, CalDAV-verberne.

**Røgtestene i `web/e2e/`** kører en rigtig browser mod den rigtige binary, med
frontenden bygget og indlejret som en del af kørslen — så de aldrig tester en
gammel frontend. Seks flows: log ind, hurtig tilføjelse, luk en opgave, klik hvert
link i sidebjælken, hver fane under indstillinger, og at manifestets ikoner og
service workeren faktisk findes. De fandt fire fejl, første gang de blev kørt.

De er bevidst kun Chromium: en røgtest findes for at fange en build, der slet ikke
virker, og at køre den i tre motorer finder den samme fejl tre gange.

**`docs/openapi.yaml` tjekkes to steder:** CI validerer at det er et gyldigt
OpenAPI-dokument, og `internal/httpapi/openapi_test.go` går routeren igennem og
fejler både på en rute uden beskrivelse og på en beskrivelse uden rute.

**Implementeret mod specifikation, men aldrig kørt mod den rigtige tjeneste:**

- **Web Push** — hele kæden findes nu, men er aldrig set virke: krypteringen er
  skrevet direkte mod RFC 8291, og klientsiden er kun kørt i en headless browser,
  hvor notifikationer er blokerede, så den ramte kun "browseren har sagt
  nej"-grenen. Hvis noget ikke virker, er krypteringen stedet at begynde.
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

## Huller

De syv, der stod her, er lukket på nær ét. Det, der er tilbage, kræver
legitimationsoplysninger og rigtige tjenester — ikke kode.

### 1. Gmail mod en rigtig konto

Det eneste hul fra den oprindelige liste, der ikke er lukket. Registrér en
OAuth-klient, sæt `VERDANDE_GMAIL_CLIENT_ID` og `_SECRET`, og kør flowet igennem
fra **Indstillinger → Integrationer**. Den mest sandsynlige fejl er
redirect-URI'en, som udledes af `VERDANDE_BASE_URL` og skal matche det
registrerede *præcist*.

### 2. Web Push mod en rigtig push-tjeneste

Klientsiden findes nu: `web/static/sw.js` viser beskeden, `web/src/lib/push.js`
abonnerer, og **Indstillinger → Notifikationer** slår det til. Krypteringen på
serveren er stadig kun skrevet mod RFC 8291 og aldrig set fra den anden side. Slå
det til i en rigtig browser over HTTPS og få serveren til at sende én.

Bemærk at det kræver HTTPS eller localhost — og at browseren kun spørger én gang.
Har man sagt nej, kan siden ikke spørge igen; det skal laves om i browserens egne
indstillinger. Fladen siger det, i stedet for at se ud som om knappen er i stykker.

### 3. PNG-ikoner til PWA'en

Manifestet peger stadig kun på `icon.svg`. Chrome og Firefox installerer fint på
det; Safari vil have en PNG som `apple-touch-icon`. Der er ingen SVG-renderer på
maskinen, så referencerne blev fjernet frem for at pege på filer, der ikke findes.
Røgtesten tjekker at hvert ikon i manifestet faktisk kan hentes, så tilføjer man
en reference uden filen, fejler CI.

### 4. AI-adapterne mod en rigtig nøgle

Anthropic-, OpenAI- og Google-formerne er skrevet efter deres dokumentation og
aldrig kørt. **Indstillinger → AI** sætter dem op; sæt en nøgle ind og bed om et
ugentligt overblik.

### 5. Sessionsliste i indstillinger

`last_seen_at` skrives netop for at kunne vise "denne enhed, for 2 minutter
siden", og der er ingen visning, der bruger det. Der er heller ikke noget endpoint
— kun `store`-laget ved, at kolonnen findes.

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

**Frontenden i binaryen er en kopi.** `cmd/verdande/webbuild/` er hvad `-tags
embedweb` indlejrer, og den bliver ikke opdateret af `npm run build`. Kører man en
binary uden at kopiere først, tester man den frontend, der lå der sidst — hvilket
under udvikling næsten altid er den forkerte. Røgtestene bygger og kopierer selv,
netop derfor.

**Playwright genindlæser sin config i hver worker.** Alt på modulniveau kører
altså igen, mens serveren kører. En `rmSync` af datamappen der sletter databasen
under den — og det fejler ikke højlydt: allerede åbne forbindelser bliver ved med
at virke mod den slettede inode, så kun de requests, der har brug for en *ny*
forbindelse, fejler. Det ligner en flaky auth-fejl. Vagten er
`process.env.TEST_WORKER_INDEX === undefined`.

**`svelte-check` er ikke i CI og fejler på hundredvis af linjer.** Det er en
JS-kodebase uden typeannotationer, så næsten alt, den siger, er "implicit any".
Brug den ikke som portvagt; `npm run build` og røgtestene er signalet.

---

## Publicering

Repoet er privat, og GHCR-pakken er det også — se [Registret er
privat](#registret-er-privat). Alt til at gøre repoet offentligt ligger klar:
MIT-licens, CONTRIBUTING, SECURITY, issue-skabeloner, dokumentation og landing
page.

Udgivelse sker ved at pushe et semver-tag; `release.yml` bygger og publicerer
`{version}`, `{major}.{minor}`, `{major}` og `latest` for amd64 og arm64. En Rune
pinned til `:0` får patches uden at krydse en major.

Pages-deployment ligger bag `workflow_dispatch` — dokumentationen bygges ved hvert
push, men intet publiceres, før du kører workflowet manuelt. Landing pagen i
`site/` er statisk og klar til Cloudflare Pages; den skal bare pege på et domæne.

**verdande.app**, **verdande.dev** og **verdande.io** var alle ledige, da navnet
blev valgt. De er ikke købt.

---

## Hvor denne session slap

`v0.1.0` og `v0.2.0` er tagget og publiceret; `latest` peger på 0.2.0. Hullerne
fra den oprindelige overlevering er lukket på nær dem, der kræver rigtige
tjenester — de står under [huller](#huller).

Fem fejl blev fundet undervejs, alle af røgtestene, og alle rettet: samtidige
skrivninger gav `SQLITE_BUSY`, en opgave man lukkede beholdt sin række, den samme
opgave kunne havne to gange i listen, en databasefejl loggede folk ud i stedet for
at fejle ærligt, og sidebjælke-testen talte links, før siden var hydreret — den
sidste fandt CI, ikke maskinen her. De tre første var der fra begyndelsen; ingen
af dem kunne ses fra en Go-test. Det er argumentet for at have røgtestene, sagt
kortere end forgængeren sagde det.

Tilføjet ud over hullerne: `PATCH /auth/me` (navn, tidszone og sprog kunne ikke
ændres nogen steder), og hele `/tokens`-fladen — `docs/api.md` henviste til
&ldquo;Settings → API tokens&rdquo;, men der fandtes hverken UI eller endpoints,
kun `store`-laget.

To ting venter på et menneske. GHCR-pakken skal sættes til offentlig i web-UI'et;
GitHub har intet API til det, og indtil da kan Yggdrasil ikke hente imaget —
se [ovenfor](#ghcr-pakken-skal-være-offentlig). Og der ligger en 8,9 MB kompileret
macOS-binary ved navn `shots` i repo-roden, committet ved et uheld i `a8e523b`;
kilden i `tools/shots/` er det, der skal bruges, og `go build ./tools/shots` uden
`-o` overskriver den sporede fil.
