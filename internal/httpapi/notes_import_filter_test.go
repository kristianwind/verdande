package httpapi

import "testing"

// En titel med tre prikker i er ikke et forsøg på at skrive uden for mappen.
//
// `strings.Contains(name, "..")` var den rigtige idé rettet mod det forkerte:
// den beskriver ikke en sti, der klatrer ud af arkivet, men ethvert navn med to
// prikker ved siden af hinanden. Noten "Så blev det endelig jul! Stay tuned... ☕️"
// blev derfor droppet lydløst — én ud af tolv hundrede, og kun fundet ved at
// tælle efter, hvor mange der kom ud.
func TestSkipFileTakesEllipsesButRefusesTraversal(t *testing.T) {
	keep := []string{
		"Så blev det endelig jul! Stay tuned... ☕️.md",
		"Møde 12.8..md",
		"vedhaeftninger/1-2-Pasted Graphic.png",
		"noter/Referat....md",
	}
	for _, name := range keep {
		if skipFile(name) {
			t.Errorf("skipFile(%q) = true, men det er et almindeligt filnavn", name)
		}
	}

	drop := []string{
		"../uden for.md",
		"noter/../../etc/passwd",
		"/etc/passwd",
		"__MACOSX/._noget",
		".skjult.md",
		"noter/._AppleDouble.md",
	}
	for _, name := range drop {
		if !skipFile(name) {
			t.Errorf("skipFile(%q) = false, men den skal afvises", name)
		}
	}
}
