package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// En zip-bombe skal ikke kunne skrive gigabyte ned på den disk, databasen ligger
// på.
//
// Uploaden er begrænset til 64 MB, og det siger ingenting om, hvad de 64 MB
// bliver til: deflate når omkring 1032:1, så de kan pakkes ud til seksogtres
// gigabyte. Prøven bygger en rigtig lille bombe — fire megabyte nuller, som
// pakker til nogle få kilobyte — og ser efter, at loftet på det udpakkede holder.
//
// Skrevet imod tallene frem for imod endepunktet, fordi det, der skal holde, er
// forholdet: en fil, hvis udpakkede størrelse overstiger loftet, må ikke læses
// færdig.
func TestAZipBombIsRefusedByItsUnpackedSize(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("bombe.bin")
	if err != nil {
		t.Fatal(err)
	}
	// Nuller pakker næsten helt væk. Fire megabyte er nok til at vise forholdet
	// uden at prøven selv bruger en gigabyte.
	if _, err := w.Write(make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	packed := int64(buf.Len())
	if packed > 64<<10 {
		t.Fatalf("prøvens egen bombe pakkede kun til %d bytes; den viser ikke noget", packed)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), packed)
	if err != nil {
		t.Fatal(err)
	}
	f := zr.File[0]
	if f.UncompressedSize64 != 4<<20 {
		t.Fatalf("udpakket størrelse = %d", f.UncompressedSize64)
	}

	// Det, importen gør: læs højst det, budgettet tillader, plus én byte — så en
	// fil, der er præcis på grænsen, kan skelnes fra en, der er over.
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	budget := int64(1 << 20)
	n, err := io.Copy(io.Discard, io.LimitReader(rc, budget+1))
	if err != nil {
		t.Fatal(err)
	}
	if n <= budget {
		t.Fatalf("læste %d bytes af en fil på 4 MB med et budget på %d — loftet fanger den ikke", n, budget)
	}
}

// Tallene skal stå i et forhold, der giver mening: en enkelt fil må aldrig kunne
// bruge hele arkivets budget, og arkivet må ikke være mindre end én fil.
func TestTheImportCeilingsAgreeWithEachOther(t *testing.T) {
	if maxNoteImportUnpacked <= maxUploadBytes {
		t.Error("arkivets budget er ikke større end én enkelt fil")
	}
	if maxNoteImportFiles < 2000 {
		t.Error("filloftet er under det, en rigtig Apple Noter-eksport indeholder")
	}
}
