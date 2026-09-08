package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/ai"
	"github.com/kristianwind/verdande/internal/quickadd"
	"github.com/kristianwind/verdande/internal/store"
)

// En mail, der bliver til en brugbar opgave.
//
// Uden det her hedder opgaven, hvad mailen hed: "Anders Jensen: SV: SV: Vedr.
// levering uge 12". Det er nok til at genkende den og ikke nok til at gøre noget
// ved den — og en indbakke af den slags linjer er præcis den bunke, man havde
// mail for at slippe for.
//
// Ét kald pr. gennemløb frem for ét pr. mail. Femogtyve mails er femogtyve gange
// ventetid og femogtyve gange betaling for det samme spørgsmål, og et gennemløb,
// der tager fem minutter, er et, der overlapper med det næste.
//
// Falder det ud, er der ikke sket noget: opgaven hedder afsender og emne, som den
// altid har gjort. En model, der er nede, må ikke kunne stoppe posten.

// mailForAI er én mail, som modellen får den at se.
type mailForAI struct {
	From    string
	Subject string
	Snippet string
}

// mailTaskLines beder modellen om én linje pr. mail, i quick-add-syntaks.
//
// Returnerer en tabel fra mailens plads i listen til linjen. Ikke en liste, fordi
// en model, der springer en mail over, ellers ville flytte alle de følgende
// linjer én plads — og så ville en opgave hedde noget, der stod i en anden mail.
// Det er den samme forsigtighed som ved oprydningen i indbakken, og den samme
// grund: en forskydning på én er den slags fejl, ingen opdager.
func (s *Server) mailTaskLines(ctx context.Context, user *store.User, mails []mailForAI) map[int]string {
	if len(mails) == 0 {
		return nil
	}
	cfg, err := s.aiConfigFor(ctx, user)
	if err != nil || !cfg.Configured() {
		return nil
	}

	lines := make([]string, 0, len(mails))
	for i, m := range mails {
		line := strconv.Itoa(i+1) + ". " + m.From + " — " + m.Subject
		if m.Snippet != "" {
			line += "\n   " + firstWords(m.Snippet, 40)
		}
		lines = append(lines, line)
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	today := time.Now().In(userLocation(user.Timezone)).Format("2006-01-02")
	out, err := ai.New(cfg).TasksFromMail(ctx, lines, today, user.Locale)
	if err != nil {
		s.log.Info("mail tasks without the model", "err", err, "user", user.ID)
		return nil
	}

	byIndex := map[int]string{}
	for i, sug := range out {
		n, line := numberedLine(sug.Line)
		if n == 0 {
			n = i + 1
		}
		if n < 1 || n > len(mails) || strings.TrimSpace(line) == "" {
			continue
		}
		byIndex[n-1] = line
	}
	return byIndex
}

// applyMailLine lægger modellens linje ind i opgaven, hvis der er en.
//
// Linjen går gennem quick-add-parseren som alt andet, så en mail, hvor der står
// "inden fredag", får en dato — og "#Firma" i en linje, modellen fandt på, flytter
// ingenting, for projektet slås op blandt dem, der findes.
//
// Emnet bliver stående i beskrivelsen. Modellen har skrevet en titel ud fra det,
// og hvis den har misforstået mailen, skal det oprindelige stadig kunne læses ved
// siden af — ellers er den eneste vej tilbage at finde mailen frem igen.
func (s *Server) applyMailLine(ctx context.Context, user *store.User, task *store.Task, line, subject string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	parsed := quickadd.Parse(line, time.Now().In(userLocation(user.Timezone)), user.Locale)
	if strings.TrimSpace(parsed.Content) == "" {
		return
	}

	task.Content = parsed.Content
	if parsed.Priority != 0 {
		task.Priority = parsed.Priority
	}
	if parsed.DueDate != "" {
		task.DueDate = parsed.DueDate
	}
	if parsed.Project != "" {
		if id, err := s.db.ProjectByName(ctx, user.ID, parsed.Project); err == nil {
			if _, err := store.RequireProjectRole(ctx, s.db, id, user.ID, store.RoleEditor); err == nil {
				task.ProjectID = id
			}
		}
	}
	if subject != "" {
		task.Description = strings.TrimSpace(subject + "\n\n" + task.Description)
	}
}
