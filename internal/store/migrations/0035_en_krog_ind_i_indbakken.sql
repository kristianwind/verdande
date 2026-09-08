-- En personlig adresse, andre programmer kan skubbe en opgave ind ad.
--
-- Der var to veje ind i forvejen, og ingen af dem passer til "gem den her, mens
-- jeg står med telefonen i hånden". API-tokenet giver adgang til alt og skal have
-- en JSON-krop skrevet rigtigt; mailadressen virker først, når nogen har sat en
-- mailserver op, og går gennem en indbakke, der kan være minutter om det. Det, der
-- manglede, var en URL, man kan smide en linje tekst efter.
--
-- Sit eget token frem for at låne mailens. De to er den samme slags hemmelighed,
-- men de bliver kompromitteret hver for sig: en URL, der har ligget i en genvej på
-- en telefon, der blev væk, skal kunne skiftes uden at mailadressen — som står i
-- andres adressebøger — skifter med. Og omvendt.
--
-- Ikke et API-token. Et token herfra kan én ting: lave en opgave i den persons
-- indbakke. Det er hele forskellen på en hemmelighed, man kan lægge i et script på
-- en anden maskine, og en, man ikke kan.
ALTER TABLE users ADD COLUMN hook_token TEXT;

-- Opslaget er "hvem er det her token", og det er også dét, der skal være entydigt:
-- to konti med det samme token er to konti, en opgave kan lande i.
CREATE UNIQUE INDEX idx_users_hook_token ON users (hook_token) WHERE hook_token IS NOT NULL;
