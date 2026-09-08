package store

import (
	"sort"
	"strings"
	"unicode"
)

// MentionsIn reads the people named with an @ out of a body, and returns their
// ids in the order they are written.
//
// Slået op i navnene frem for tolket ud af teksten. Et navn kan have mellemrum,
// bindestreg og punktum i sig, og et udtryk, der skulle fange dem alle, ville
// også fange sætningen bagefter: `@Sofie i morgen` ville blive til en person, der
// hedder "Sofie i morgen", og ellers til "Sofie", alt efter hvor grådigt det var
// skrevet. Her er der ikke noget at gætte — der står et navn, eller der gør ikke.
//
// Det er også dét, der gør det forsvarligt at *dele* på: en omtale giver adgang,
// og adgang må ikke afhænge af, hvor langt et regulært udtryk valgte at læse.
//
// Længste navn først, så "@Sofie Jensen" bliver Sofie Jensen og ikke Sofie med et
// efternavn hængende bagefter — også når begge findes som konti.
func MentionsIn(body string, people []Person) []string {
	if body == "" || len(people) == 0 {
		return nil
	}
	lower := strings.ToLower(body)

	byLength := make([]Person, len(people))
	copy(byLength, people)
	sort.SliceStable(byLength, func(i, j int) bool {
		return len(byLength[i].Name) > len(byLength[j].Name)
	})

	// Hvilke tegn der allerede er læst som en del af en omtale. Uden det ville
	// "@Sofie Jensen" også tælle som en omtale af Sofie, hvis begge er konti — og
	// noten ville blive delt med en, der ikke står i den.
	claimed := make([]bool, len(body))
	found := map[string]int{} // bruger -> hvor i teksten, så rækkefølgen kan bæres

	for _, p := range byLength {
		name := strings.ToLower(strings.TrimSpace(p.Name))
		if name == "" || p.ID == "" {
			continue
		}
		needle := "@" + name
		for at := 0; ; {
			i := strings.Index(lower[at:], needle)
			if i < 0 {
				break
			}
			i += at
			at = i + len(needle)

			end := i + len(needle)
			if overlaps(claimed, i, end) || !isMention(body, i, end) {
				continue
			}
			for j := i; j < end; j++ {
				claimed[j] = true
			}
			if _, seen := found[p.ID]; !seen {
				found[p.ID] = i
			}
		}
	}

	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return found[out[i]] < found[out[j]] })
	return out
}

func overlaps(claimed []bool, from, to int) bool {
	for i := from; i < to && i < len(claimed); i++ {
		if claimed[i] {
			return true
		}
	}
	return false
}

// isMention er de to kanter om navnet.
//
// Foran: begyndelsen af noten eller noget, der ikke er en bogstavstavelse — så en
// e-mailadresse ikke bliver til en omtale, fordi der tilfældigvis står et navn
// efter snabel-a'et.
//
// Bagved: ikke midt i et længere ord. "@Sofies kalender" er skrevet om Sofie og
// tæller med — ejefaldets s hører til sætningen, ikke til en anden person — men
// "@Sofienberg" er et sted og ikke en, der skal have noten delt. Grænsen er sat
// ved, at et bogstav *lige* efter navnet kun må være et ejefalds-s.
func isMention(body string, from, to int) bool {
	runes := []rune(body[:from])
	if len(runes) > 0 {
		prev := runes[len(runes)-1]
		if unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == '@' {
			return false
		}
	}

	rest := body[to:]
	if rest == "" {
		return true
	}
	next := []rune(rest)
	if next[0] == 's' || next[0] == 'S' {
		next = next[1:]
		if len(next) == 0 {
			return true
		}
	}
	return !unicode.IsLetter(next[0]) && !unicode.IsDigit(next[0])
}
