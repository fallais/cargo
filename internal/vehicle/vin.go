// Package vehicle identifies the car on the other end of the adapter.
//
// Which vehicle we are attached to is not cosmetic: manufacturer trouble codes
// mean different things on different marques, so without a make the catalog
// cannot answer for roughly 700 of them. Reading the VIN turns that from a
// question the user has to answer into one the car answers itself.
package vehicle

import (
	"fmt"
	"strings"
)

// vinLength is fixed by ISO 3779.
const vinLength = 17

// yearLetters is the model-year cycle used in position 10. I, O, Q, U and Z
// are excluded because they are too easily confused with digits.
const yearLetters = "ABCDEFGHJKLMNPRSTVWXY"

// transliteration maps VIN letters to their check-digit values. I, O and Q are
// absent because they cannot appear in a VIN at all.
var transliteration = map[byte]int{
	'A': 1, 'B': 2, 'C': 3, 'D': 4, 'E': 5, 'F': 6, 'G': 7, 'H': 8,
	'J': 1, 'K': 2, 'L': 3, 'M': 4, 'N': 5, 'P': 7, 'R': 9,
	'S': 2, 'T': 3, 'U': 4, 'V': 5, 'W': 6, 'X': 7, 'Y': 8, 'Z': 9,
}

// checkWeights are the positional weights for the check digit. Position 9 is
// the check digit itself and so weighs nothing.
var checkWeights = [vinLength]int{8, 7, 6, 5, 4, 3, 2, 10, 0, 9, 8, 7, 6, 5, 4, 3, 2}

// VIN is a validated vehicle identification number.
type VIN struct {
	Raw string
	// WMI is the world manufacturer identifier, positions 1 to 3.
	WMI string
	// Make is the marque the WMI resolves to, empty if unrecognised.
	Make string
	// Year is the model year, zero if it could not be determined.
	Year int
	// CheckDigitValid reports whether position 9 agrees with the rest.
	// Only North American VINs are required to carry one, so a false here
	// is a hint rather than a verdict.
	CheckDigitValid bool
}

// ParseVIN validates and decodes a VIN.
func ParseVIN(raw string) (VIN, error) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	raw = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, raw)

	if len(raw) != vinLength {
		return VIN{}, fmt.Errorf("vin: %q is %d characters, want %d", raw, len(raw), vinLength)
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z' && c != 'I' && c != 'O' && c != 'Q':
		default:
			return VIN{}, fmt.Errorf("vin: %q contains an invalid character %q", raw, c)
		}
	}

	v := VIN{
		Raw:             raw,
		WMI:             raw[:3],
		CheckDigitValid: validCheckDigit(raw),
	}
	v.Make = MakeForWMI(v.WMI)
	v.Year = modelYear(raw)
	return v, nil
}

// validCheckDigit verifies position 9.
func validCheckDigit(vin string) bool {
	sum := 0
	for i := 0; i < vinLength; i++ {
		c := vin[i]

		var value int
		switch {
		case c >= '0' && c <= '9':
			value = int(c - '0')
		default:
			v, ok := transliteration[c]
			if !ok {
				return false
			}
			value = v
		}
		sum += value * checkWeights[i]
	}

	want := byte('0' + sum%11)
	if sum%11 == 10 {
		want = 'X'
	}
	return vin[8] == want
}

// modelYear decodes position 10.
//
// The letter cycle repeats every thirty years, so 'A' is both 1980 and 2010.
// The convention for resolving that is position 11: it is alphabetic on
// vehicles from 2010 onward and numeric before. It is a convention rather than
// a rule, so a wrong answer here is possible and the year is only ever a hint.
func modelYear(vin string) int {
	code := vin[9]

	if code >= '1' && code <= '9' {
		year := 2000 + int(code-'0')
		if vin[6] >= 'A' && vin[6] <= 'Z' {
			year += 30
		}
		return year
	}

	index := strings.IndexByte(yearLetters, code)
	if index < 0 {
		return 0
	}

	year := 1980 + index
	if vin[6] >= 'A' && vin[6] <= 'Z' {
		year += 30
	}
	return year
}

// wmiPrefixes maps world manufacturer identifiers to marques.
//
// The full registry runs to thousands of entries and is not published freely,
// so this covers the makes the trouble-code catalog can actually say something
// about, plus the common European marques. An unrecognised WMI is not an
// error: the user picks the make instead.
var wmiPrefixes = map[string]string{
	// General Motors
	"1G1": "chevy", "1GC": "chevy", "2G1": "chevy", "3GN": "chevy", "KL1": "chevy",
	"1G4": "buick", "2G4": "buick", "1G6": "cadillac", "1GY": "cadillac",
	"1GK": "gmc", "1GT": "gmc", "2GK": "gmc",
	"1G3": "oldsmobile", "1G2": "pontiac", "2G2": "pontiac",
	"1G8": "saturn", "5GZ": "saturn", "2CN": "geo",

	// Ford
	"1FA": "ford", "1FB": "ford", "1FC": "ford", "1FD": "ford", "1FM": "ford",
	"1FT": "ford", "2FA": "ford", "2FM": "ford", "3FA": "ford", "WF0": "ford",
	"1LN": "lincoln", "5LM": "lincoln", "1ME": "mercury", "4M2": "mercury",

	// Chrysler group
	"1C3": "chrysler", "1C4": "chrysler", "2C3": "chrysler", "3C4": "chrysler",
	"1B3": "dodge", "1D7": "dodge", "2B3": "dodge", "3D7": "dodge", "1D4": "dodge",
	"1J4": "jeep", "1J8": "jeep", "1C6": "jeep", "3C6": "jeep",
	"1P3": "plymouth", "2P4": "plymouth",

	// Japanese
	"1HG": "honda", "2HG": "honda", "JHM": "honda", "SHH": "honda", "3HG": "honda",
	"19U": "acura", "JH4": "acura",
	"4T1": "toyota", "5TD": "toyota", "JTD": "toyota", "JTE": "toyota", "2T1": "toyota",
	"JTH": "lexus", "JTJ": "lexus",
	"1N4": "nissan", "1N6": "nissan", "3N1": "nissan", "JN1": "nissan", "JN8": "nissan",
	"JNK": "infiniti", "JNR": "infiniti", "5N3": "infiniti",
	"JM1": "mazda", "JM3": "mazda", "4F2": "mazda", "4F4": "mazda",
	"JF1": "subaru", "JF2": "subaru", "4S3": "subaru", "4S4": "subaru",
	"JA3": "mitsubishi", "JA4": "mitsubishi", "4A3": "mitsubishi", "4A4": "mitsubishi",
	"JS2": "suzuki", "JS3": "suzuki", "KL5": "suzuki",

	// Korean
	"KNA": "kia", "KND": "kia", "KNM": "kia", "5XY": "kia",

	// European
	"WBA": "bmw", "WBS": "bmw", "WBY": "bmw", "4US": "bmw", "5UX": "bmw",
	"WDB": "mercedes", "WDC": "mercedes", "WDD": "mercedes", "WDF": "mercedes",
	"4JG": "mercedes", "W1K": "mercedes", "W1N": "mercedes",
	"WVW": "volkswagen", "WV1": "volkswagen", "WV2": "volkswagen",
	"1VW": "volkswagen", "3VW": "volkswagen", "9BW": "volkswagen",
	"WAU": "audi", "WA1": "audi", "TRU": "audi", "WUA": "audi",
	"SAJ": "jaguar", "SAD": "jaguar",
}

// MakeForWMI resolves a world manufacturer identifier to a marque, or "" if it
// is not in the table.
func MakeForWMI(wmi string) string {
	return wmiPrefixes[strings.ToUpper(strings.TrimSpace(wmi))]
}
