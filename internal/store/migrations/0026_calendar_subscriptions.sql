-- Abonnement på en kalender, man kun har en adresse til.
--
-- Google-vejen er OAuth: én konto pr. person, hvor tokenet er identiteten, og
-- `calendar_accounts_once` sørgede for netop det — én række pr. (person,
-- udbyder). Et abonnement er en anden slags ting. Der er intet token og ingen
-- konto, kun en adresse, og man har flere: helligdage, en fodboldklub, en
-- fælleskalender fra arbejdet. Det er derfor det blev bedt om som "flere
-- kalendere".
--
-- Samme tabel frem for en ny. Et abonnement er én kalender med begivenheder i, og
-- det er præcis det, `calendars` og `calendar_events` allerede beskriver — med
-- vinduet, `shown`, farven og hele fejemaskinen omkring sig. En tabel til ville
-- være en anden vej til det samme sted, og to veje til ét sted er to steder at
-- rette den dag noget om begivenheder ændrer sig.
--
-- Adressen bliver identiteten, som tokenet er det for Google. To rækker med den
-- samme adresse for den samme person ville være den samme kalender hentet to
-- gange, og der er ingen måde at sige hvilken der gælder.
ALTER TABLE calendar_accounts ADD COLUMN url TEXT NOT NULL DEFAULT '';

-- Det gamle indeks holder stadig for OAuth-rækkerne — dem uden adresse — og
-- slipper abonnementerne fri. Delvise indekser er den ene ting SQLite kan her,
-- som en tabelombygning ellers skulle til for.
DROP INDEX calendar_accounts_once;

CREATE UNIQUE INDEX calendar_accounts_once
    ON calendar_accounts (user_id, provider) WHERE url = '';

CREATE UNIQUE INDEX calendar_subscription_once
    ON calendar_accounts (user_id, url) WHERE url <> '';
