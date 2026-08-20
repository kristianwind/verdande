-- verdande:rebuild-tables
--
-- Notekroppe forsegles.
--
-- Grunden er den samme som for postkassers kodeord og OAuth-tokens: en
-- sikkerhedskopi kan hentes gennem fladen, og indtil nu bar hver eneste af dem
-- hver eneste note i klartekst. Noter er dér, folk skriver kodeord og API-nøgler
-- ned — det ved vi, fordi der ligger sådan nogle i denne installation.
--
-- # Hvorfor det ikke var gjort for længe siden
--
-- Kroppen var kopieret to steder mere, og begge kopier var klartekst:
--
--   * `fold` — en genereret søjle med title || ' ' || body, translittereret, så
--     "grøn" kan findes ved at skrive "gron".
--   * `notes_fts` — et FTS5-indeks over titel, krop og fold.
--
-- At forsegle kroppen og lade dem stå ville have været teater: en angriber med
-- filen kunne læse hver note ud af indekset. Så de skal begge væk, og søgningen
-- skal skrives om til at åbne teksten i Go.
--
-- # Hvad det koster, og hvornår formen er forkert
--
-- Søgningen læser nu hver note. Målt: tolv hundrede noter à 3,3 kB koster 33–43 ms
-- pr. søgning, og fladen venter 250 ms efter sidste tastetryk, før den spørger — så
-- det ligger inde i pausen. Tiden går ikke med at åbne konvolutterne, men med at
-- folde og småskrive hver krop; det er lineært, så regn med omtrent 350 ms ved ti
-- tusind noter. Dér er det den forkerte form, og svaret er da et *nøglet* indeks:
-- en HMAC pr. ord under den samme nøgle. Bemærk hvad det koster, når det bliver
-- aktuelt — et nøglet indeks kan slå et helt ord op og ikke andet, så præfiks- og
-- delordssøgningen, der findes i dag, ville falde bort.
--
-- # Hvad der stadig ligger i klartekst
--
-- Alt andet. Opgavetitler, beskrivelser, kommentarer — og `note_links`, som er
-- id'er udledt af kroppen, så en fil røber stadig *hvilke* projekter og opgaver en
-- note peger på, men ikke hvad der står. Det er skrevet i SECURITY.md, og det er
-- ikke løst her.
--
-- # Selve ombygningen
--
-- SQLite kan ikke fjerne en genereret søjle på plads, så tabellen bygges om.
-- Rækkerne kopieres med kroppen, som den er: forseglingen sker i Go, hvor nøglen
-- er, og en opstart efter denne migration går rækkerne igennem og lukker dem. Til
-- den tid kan en søjle indeholde begge dele, og `v1:`-præfikset findes netop for
-- at kunne læses uden at gætte.
CREATE TABLE notes_sealed (
    id          TEXT PRIMARY KEY,
    project_id  TEXT REFERENCES projects (id) ON DELETE CASCADE,
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    created_by  TEXT REFERENCES users (id) ON DELETE SET NULL,
    pinned      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER,
    archived_at INTEGER
);

INSERT INTO notes_sealed
    (id, project_id, title, body, created_by, pinned, created_at, updated_at, deleted_at, archived_at)
SELECT id, project_id, title, body, created_by, pinned, created_at, updated_at, deleted_at, archived_at
FROM notes;

DROP TRIGGER notes_fts_insert;
DROP TRIGGER notes_fts_delete;
DROP TRIGGER notes_fts_update;
DROP TABLE notes_fts;
DROP TABLE notes;

ALTER TABLE notes_sealed RENAME TO notes;

CREATE INDEX notes_by_project ON notes (project_id, deleted_at, updated_at DESC);
CREATE INDEX notes_current ON notes (archived_at, deleted_at, updated_at DESC)
    WHERE deleted_at IS NULL;
