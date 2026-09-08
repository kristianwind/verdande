-- En besked kan handle om en note.
--
-- Tabellen kunne pege på et projekt og en opgave, fordi det var de to ting, der
-- kunne ske noget med. Nu kan der også: en note, der er delt, bliver rettet af en
-- anden, og den, den er delt med, skal kunne se at det er sket — uden at åbne den
-- for at opdage det.
--
-- En søjle ved siden af de to andre frem for et generisk (kind, id)-par. De tre er
-- ikke det samme slags nul: en besked om en opgave har *også* et projekt, og en om
-- en note har måske ingen af delene. Et par ville skulle tolkes hvert sted, det
-- læses, og fremmednøglen — som er dén, der rydder beskeden op, når noten slettes
-- — kan et generisk par slet ikke have.
ALTER TABLE notifications ADD COLUMN note_id TEXT REFERENCES notes (id) ON DELETE CASCADE;

-- Beskeder om den samme note bliver slået sammen, mens de er ulæste, og opslaget
-- er nøjagtig det: min, ulæst, om denne note.
CREATE INDEX notifications_unread_by_note
    ON notifications (user_id, note_id) WHERE read_at IS NULL AND note_id IS NOT NULL;
