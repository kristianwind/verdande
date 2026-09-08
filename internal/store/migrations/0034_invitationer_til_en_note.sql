-- En invitation kan handle om en note.
--
-- En note kunne deles med nogen, der allerede havde en konto — og kun med dem.
-- Skulle en, der ikke havde en, læse med, skulle man først invitere dem til
-- instansen, vente på at de oprettede sig, og så finde noten frem og dele den. Tre
-- skridt og en ventetid for det, der er én sætning: "lad Sofie se den her".
--
-- Rækken er den, der allerede findes. En invitation bærer i forvejen en e-mail, en
-- rolle og et forseglet token; det eneste, der manglede, var at kunne sige, hvad
-- den er en invitation *til*. project_id sagde det for projekter, og note_id siger
-- det nu for noter.
--
-- To søjler frem for én kolonne med et id og en art ved siden af — samme valg som
-- notifications traf i 0033, og af samme grund: fremmednøglen. ON DELETE CASCADE
-- er dén, der rydder invitationen væk, når noten slettes, inden nogen når at tage
-- imod en invitation til noget, der ikke er der længere. Et generisk par kan ikke
-- have den nøgle, og oprydningen skulle så skrives i kode, der huskede at køre.
--
-- De to udelukker hinanden i praksis, men det står ikke som en CHECK: en
-- invitation uden nogen af delene er den gyldige "kom med på instansen", og en
-- regel, der skulle tillade nul eller én af to, ville skulle skrives om hver gang
-- der kom en tredje slags ting at invitere til.
ALTER TABLE invites ADD COLUMN note_id TEXT REFERENCES notes (id) ON DELETE CASCADE;

-- Panelet under en note spørger "hvem er inviteret til den her, og har ikke taget
-- imod endnu" hver gang, det åbnes. Det er opslaget, og det er derfor, det er
-- indekset. Kun de uafklarede: en invitation, der er taget imod, er en deling nu
-- og står i note_shares.
CREATE INDEX invites_pending_by_note
    ON invites (note_id) WHERE accepted_at IS NULL AND note_id IS NOT NULL;
