import { test, expect } from '@playwright/test';
import { readList } from '../src/lib/pastelist.js';

/**
 * Læsningen af en indsat liste, prøvet for sig.
 *
 * Reglerne er små og mange, og de er billigere at stille her end gennem en
 * browser: hvad er et mærke, hvad er indhold, og hvor dybt står en linje. Flowet
 * gennem fladen har sin egen prøve i smoke.spec.js — den viser, at det hænger
 * sammen, ikke at hvert tilfælde er rigtigt.
 */
test.describe('en indsat liste læses som opgaver og underopgaver', () => {
	test('mærker fjernes, teksten bliver', () => {
		expect(readList('- køb mælk\n* ring til Per\n+ hent pakken\n')).toEqual([
			{ depth: 0, text: 'køb mælk', done: false },
			{ depth: 0, text: 'ring til Per', done: false },
			{ depth: 0, text: 'hent pakken', done: false }
		]);
	});

	test('nummererede lister er også lister', () => {
		expect(readList('1. først\n2) så\n10. til sidst')).toEqual([
			{ depth: 0, text: 'først', done: false },
			{ depth: 0, text: 'så', done: false },
			{ depth: 0, text: 'til sidst', done: false }
		]);
	});

	test('en afkrydsning er indhold, ikke notation', () => {
		// Det er hele grunden til at læse boksen: en halvt afkrydset liste, der
		// kommer ind som lutter uløste opgaver, har tabt det, man indsatte den for.
		expect(readList('- [x] betalt\n- [ ] ikke betalt\n- [X] også betalt')).toEqual([
			{ depth: 0, text: 'betalt', done: true },
			{ depth: 0, text: 'ikke betalt', done: false },
			{ depth: 0, text: 'også betalt', done: true }
		]);
	});

	test('indrykning bærer niveauet, uanset hvor bred den er', () => {
		// To mellemrum i den ene liste, fire i den anden, en tabulator i den tredje:
		// alle tre betyder ét niveau. Bredden gættes ikke, den læses af teksten.
		const to = readList('a\n  b\nc');
		const fire = readList('a\n    b\nc');
		const tab = readList('a\n\tb\nc');
		const want = [
			{ depth: 0, text: 'a', done: false },
			{ depth: 1, text: 'b', done: false },
			{ depth: 0, text: 'c', done: false }
		];
		expect(to).toEqual(want);
		expect(fire).toEqual(want);
		expect(tab).toEqual(want);
	});

	test('en liste, der er rykket ind som helhed, begynder stadig på niveau nul', () => {
		// Kopierer man fra et dokument, følger der ofte indrykning med på alle
		// linjer. Den er ikke et niveau — der er ikke noget at være under.
		expect(readList('    a\n      b')).toEqual([
			{ depth: 0, text: 'a', done: false },
			{ depth: 1, text: 'b', done: false }
		]);
	});

	test('tomme linjer er luft i det kopierede, ikke punkter', () => {
		// Og de nulstiller ikke niveauet: en liste med luft mellem punkterne er én
		// liste.
		expect(readList('a\n\n  b\n\n  c')).toEqual([
			{ depth: 0, text: 'a', done: false },
			{ depth: 1, text: 'b', done: false },
			{ depth: 1, text: 'c', done: false }
		]);
	});

	test('teksten røres ikke ud over mærket', () => {
		// Datoer, #projekt og @etiket læses af quickadd på serveren, præcis som når
		// linjen er skrevet i hånden. To steder at forstå en opgavelinje er ét for
		// mange.
		expect(readList('- betal moms i morgen #Firma p1\n- andet')).toEqual([
			{ depth: 0, text: 'betal moms i morgen #Firma p1', done: false },
			{ depth: 0, text: 'andet', done: false }
		]);
	});
});
