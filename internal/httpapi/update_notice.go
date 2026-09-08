package httpapi

import (
	"context"
	"strings"

	"github.com/kristianwind/verdande/internal/store"
	"github.com/kristianwind/verdande/internal/update"
)

// Besked til administratorerne, når der er kommet en ny version.
//
// Tjekket fandtes i forvejen, og svaret stod ét sted: nederst på en
// indstillingsside. Det er en oplysning, man kun får, hvis man i forvejen havde
// mistanke om, at der var noget at se efter — og "der er kommet en rettelse af
// noget, du er ramt af" er ikke noget, man gætter sig til at kigge efter.
//
// Kun til administratorer, af samme grund som versionsfeltet er det: en besked om,
// at serveren er bagud, er en besked om en andens arbejde, hvis man ikke selv kan
// gøre noget ved den. Og den fører hen, hvor knappen til at gøre det sidder.

const updateScope = "update"

// NotifyOfUpdates er timeslaget. Selve tjekket mellemlagrer sit svar i seks timer,
// så en time mellem opslagene her koster ikke noget — og en instans, der har været
// slukket, opdager det ved den første time, den er vågen.
func (s *Server) NotifyOfUpdates(ctx context.Context) error {
	return s.notifyOfUpdate(ctx, s.updates.Status(ctx))
}

// notifyOfUpdate tager status som argument frem for at hente den, så den kan
// prøves uden at spørge GitHub om noget.
func (s *Server) notifyOfUpdate(ctx context.Context, status update.Status) error {
	// Slået fra betyder slået fra. Et tjek, operatøren ikke har bedt om, må ikke
	// blive til en besked ad bagvejen.
	if status.Disabled || !status.Available || status.Latest == "" {
		return nil
	}

	stored, err := s.db.InstanceSettings(ctx, updateScope)
	if err != nil {
		return err
	}
	// Én besked pr. version, ikke én pr. dag. En påmindelse hver morgen om det
	// samme er den slags besked, folk slår fra — og så er den næste, der betyder
	// noget, også slået fra.
	if last, _ := stored["notified_version"].(string); last == status.Latest {
		return nil
	}

	users, err := s.db.ListUsers(ctx)
	if err != nil {
		return err
	}
	title := updateTitle(status.Latest)
	body := firstLineOf(status.Notes)
	for _, u := range users {
		if !u.IsAdmin {
			continue
		}
		s.notifyDirect(ctx, &store.Notification{
			UserID: u.ID, Kind: "update.available", Title: title, Body: body,
		})
	}

	// Skrevet efter beskederne, og kun hvis de blev sendt. Skrevet først ville en
	// fejl midtvejs betyde, at halvdelen fik besked og resten aldrig gjorde.
	return s.db.SetInstanceSettings(ctx, updateScope, map[string]any{
		"notified_version": status.Latest,
	})
}

// updateTitle er skrevet på dansk, fordi den også bliver til en push-besked, og
// push har ingen ordbog at slå op i. Fladen skriver sin egen sætning ud fra
// `kind` — se KINDS i Notifications.svelte.
func updateTitle(version string) string {
	return "verdande " + version + " er klar"
}

// firstLineOf er den første linje af udgivelsesnoterne, som beskedens krop.
//
// Hele teksten ville være en udgivelsesnote i en notifikation, og det er ikke det,
// en notifikation er til: den skal sige, at der er noget, og hvor man læser det.
func firstLineOf(notes string) string {
	for _, line := range strings.Split(notes, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return firstWords(line, 30)
		}
	}
	return ""
}
