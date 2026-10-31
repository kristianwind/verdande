-- At forbinde Claude med sin konto i stedet for med et token, man selv har lavet.
--
-- Claude's forbindelsesdialog prøver OAuth, før den gør noget andet, og indtil nu
-- var svaret "det findes ikke her" — så den eneste vej ind var at lave et token i
-- indstillingerne og sætte det ind i en adresse. Det virker, men det er en
-- hemmelighed, man skal kopiere mellem to programmer, og det er præcis det trin,
-- folk går i stå på.
--
-- En klient registrerer sig selv (RFC 7591) og får et id. Ingen hemmelighed: en
-- klient, der kører hos en anden, kan ikke holde på en, så det er PKCE, der beviser,
-- at det er den samme, der kommer tilbage med koden.
CREATE TABLE oauth_clients (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    -- JSON-liste. Kun adresser, klienten selv opgav ved registreringen, kan få en
    -- kode sendt hen til sig — ellers er "godkend" en knap, der sender adgangen til
    -- hvem som helst.
    redirect_uris TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

-- Det, en godkendelse udsteder, er et almindeligt API-token. Det står derfor på
-- siden med tokens, kan tilbagekaldes der, og når alle de steder, et token
-- allerede gælder. Kolonnen siger, hvilken klient det hører til, så en ny
-- godkendelse fra den samme klient erstatter den gamle frem for at lægge sig ved
-- siden af.
ALTER TABLE api_tokens ADD COLUMN oauth_client_id TEXT REFERENCES oauth_clients (id) ON DELETE CASCADE;

CREATE INDEX idx_api_tokens_oauth_client ON api_tokens (oauth_client_id) WHERE oauth_client_id IS NOT NULL;
