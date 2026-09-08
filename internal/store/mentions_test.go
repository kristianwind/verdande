package store

import (
	"reflect"
	"testing"
)

func TestMentionsIn(t *testing.T) {
	people := []Person{
		{ID: "sofie", Name: "Sofie"},
		{ID: "sofie-jensen", Name: "Sofie Jensen"},
		{ID: "kristian", Name: "Kristian"},
	}

	for _, tc := range []struct {
		name string
		body string
		want []string
	}{
		{"ingen omtale", "En helt almindelig note om noget", nil},
		{"én omtale", "@Sofie kan du kigge på det her?", []string{"sofie"}},
		{"midt i en sætning", "Aftalt med @Kristian i går", []string{"kristian"}},
		{"uden hensyn til store bogstaver", "@sofie ja", []string{"sofie"}},
		{"to omtaler i den rækkefølge de står", "@Kristian og @Sofie", []string{"kristian", "sofie"}},
		{"den samme to gange er én", "@Sofie ... og @Sofie igen", []string{"sofie"}},
		// Det lange navn vinder, og det korte tælles ikke med oveni: noten deles
		// med den, der står i den, og ikke også med en, der ikke gør.
		{"længste navn vinder", "@Sofie Jensen kigger på det", []string{"sofie-jensen"}},
		// En adresse er ikke en omtale. Ellers ville enhver note med en mailadresse
		// i sig dele sig selv med den, der tilfældigvis hedder noget efter snabel-a.
		{"e-mailadresse", "skriv til hej@Sofie.dk", nil},
		{"ejefald tæller med", "det står i @Sofies kalender", []string{"sofie"}},
		{"et længere ord gør ikke", "vi mødtes på @Sofienberg", nil},
		{"ukendt navn", "@Ingen her", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MentionsIn(tc.body, people)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MentionsIn(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// Ingen mennesker, ingen omtaler: opslaget sker i navnene, så en tom liste er et
// tomt svar frem for et gæt på, hvad et @ mon betød.
func TestMentionsInWithoutPeople(t *testing.T) {
	if got := MentionsIn("@Sofie hej", nil); got != nil {
		t.Errorf("got %v, want nothing to resolve against", got)
	}
}
