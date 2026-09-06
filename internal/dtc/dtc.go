// Package dtc decodes and describes OBD-II Diagnostic Trouble Codes as defined
// by SAE J2012.
//
// A trouble code travels on the wire as two bytes and is displayed as a letter
// plus four hex digits. The encoding is entirely self-describing: the system
// (powertrain, chassis, body, network) and whether the definition is
// ISO/SAE-controlled or vendor-specific both fall out of the first byte. That
// matters, because no open catalog covers every manufacturer code. When a
// lookup misses we can still say something true about the code rather than
// printing "unknown".
package dtc

import (
	"fmt"
	"strconv"
	"strings"
)

// System is the vehicle system a code belongs to, held in the top two bits of
// the first code byte.
type System uint8

const (
	Powertrain System = iota
	Chassis
	Body
	Network
)

var systemLetters = [4]byte{'P', 'C', 'B', 'U'}

// Letter returns the character a code of this system is printed with.
func (s System) Letter() byte { return systemLetters[s&0x3] }

func (s System) String() string {
	switch s {
	case Powertrain:
		return "Powertrain"
	case Chassis:
		return "Chassis"
	case Body:
		return "Body"
	case Network:
		return "Network"
	}
	return "Unknown"
}

// Kind says who owns a code's definition.
type Kind uint8

const (
	// Generic codes are ISO/SAE controlled and mean the same thing on every
	// vehicle, so a shared catalog can describe them.
	Generic Kind = iota
	// Manufacturer codes are vendor-defined. J2012 reserves the ranges but
	// not the meanings, which live in OEM service documentation.
	Manufacturer
	// Reserved ranges are set aside by J2012 and should not appear in the
	// wild.
	Reserved
)

func (k Kind) String() string {
	switch k {
	case Generic:
		return "generic"
	case Manufacturer:
		return "manufacturer-specific"
	case Reserved:
		return "reserved"
	}
	return "unknown"
}

// Status is the diagnostic mode a code was read from, which determines how much
// weight to give it.
type Status uint8

const (
	// Stored codes come from mode 03: a fault confirmed over enough drive
	// cycles to light the MIL.
	Stored Status = iota
	// Pending codes come from mode 07: seen once, not yet confirmed.
	Pending
	// Permanent codes come from mode 0A: they survive a battery disconnect
	// and clear only once the vehicle itself decides the fault is gone.
	Permanent
)

func (s Status) String() string {
	switch s {
	case Stored:
		return "stored"
	case Pending:
		return "pending"
	case Permanent:
		return "permanent"
	}
	return "unknown"
}

// DTC is a decoded trouble code.
type DTC struct {
	Raw    uint16 // the two bytes as they arrived, for debugging
	Code   string // canonical form, e.g. "P0301"
	System System
	Kind   Kind
	Status Status
	Module string // ECU the code was read from, e.g. "Engine" or "ABS"
}

// Decode turns the two raw bytes of a trouble code into a DTC.
//
// The first byte packs three fields:
//
//	bits 7-6  system    P / C / B / U
//	bits 5-4  digit 1   0-3
//	bits 3-0  digit 2
//
// and the second byte carries digits 3 and 4 as its high and low nibbles.
func Decode(a, b byte) DTC {
	sys := System(a >> 6)
	d1 := (a & 0x30) >> 4
	d2 := a & 0x0F

	return DTC{
		Raw:    uint16(a)<<8 | uint16(b),
		Code:   fmt.Sprintf("%c%X%X%X%X", sys.Letter(), d1, d2, (b&0xF0)>>4, b&0x0F),
		System: sys,
		Kind:   kindOf(sys, d1, d2),
		Status: Stored,
	}
}

// kindOf applies the J2012 range reservations. Digit 1 does most of the work;
// the powertrain P3 range is the one place digit 2 also matters.
func kindOf(sys System, d1, d2 byte) Kind {
	switch d1 {
	case 0:
		return Generic
	case 1:
		return Manufacturer
	case 2:
		// P2xxx returned to ISO/SAE control; B2/C2/U2 did not.
		if sys == Powertrain {
			return Generic
		}
		return Manufacturer
	case 3:
		if sys == Powertrain {
			// P30xx-P33xx are vendor space, P34xx-P39xx are ISO/SAE.
			if d2 <= 3 {
				return Manufacturer
			}
			return Generic
		}
		return Reserved
	}
	return Generic
}

// Parse reads a code in its printed form, e.g. "P0301". It is the inverse of
// Decode and is used for catalog files and user input.
func Parse(code string) (DTC, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 5 {
		return DTC{}, fmt.Errorf("dtc: %q is not five characters", code)
	}

	sys := -1
	for i, letter := range systemLetters {
		if code[0] == letter {
			sys = i
			break
		}
	}
	if sys < 0 {
		return DTC{}, fmt.Errorf("dtc: %q does not start with P, C, B or U", code)
	}

	digits, err := strconv.ParseUint(code[1:], 16, 16)
	if err != nil {
		return DTC{}, fmt.Errorf("dtc: %q has non-hex digits: %w", code, err)
	}
	if digits>>12 > 3 {
		return DTC{}, fmt.Errorf("dtc: %q has an out-of-range first digit", code)
	}

	a := byte(sys)<<6 | byte(digits>>8)
	return Decode(a, byte(digits)), nil
}

// Describe states what can be known from the encoding alone. It is the floor
// under every catalog lookup: a code we have no definition for still tells us
// its system and whether a definition could exist in a shared catalog at all.
func (d DTC) Describe() string {
	switch d.Kind {
	case Manufacturer:
		return fmt.Sprintf("%s, manufacturer-specific (consult service documentation)", d.System)
	case Reserved:
		return fmt.Sprintf("%s, reserved range", d.System)
	default:
		return fmt.Sprintf("%s, generic", d.System)
	}
}

func (d DTC) String() string { return d.Code }
