-- Sessioner fra en app.
--
-- En app logger ind som en browser gør, med adgangskode og evt. kode, og får en
-- almindelig session. Forskellen er transporten: tokenet kommer i svaret og sendes
-- som `Authorization: Bearer`, fordi en app, der henter sit UI fra sig selv, ikke
-- deler origin med serveren og derfor aldrig får cookien tilbage. At det er en
-- SESSION og ikke et API-token er hele pointen: den står i listen over enheder,
-- kan logges ud derfra, og den når indstillingerne for 2FA og nøgler, som et
-- token med vilje ikke gør.
--
-- device_name er det navn, appen giver sig selv ("Kristians iPhone"), så listen
-- siger noget, en person genkender. idle_ttl er sekunder: større end 0 betyder, at
-- sessionen glider — hver gang den bruges, flyttes udløbet frem. En browser får
-- 0 og sin faste levetid som før; en telefon, man har i lommen hver dag, må ikke
-- logge sig selv ud hver 30. dag, men en telefon, der ligger i en skuffe et halvt
-- år, skal.
ALTER TABLE sessions ADD COLUMN client TEXT NOT NULL DEFAULT 'web';
ALTER TABLE sessions ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN idle_ttl INTEGER NOT NULL DEFAULT 0;
