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

	// FailureType is the third byte of a UDS trouble code, which says how
	// the component failed rather than which one did. OBD-II codes are two
	// bytes and leave this zero.
	//
	// It is kept separate from Code because the catalog is keyed on the
	// five-character code: "C0035-64" and "C0035-1C" are the same circuit
	// failing two different ways, and both should find the same entry.
	FailureType byte
	// HasFailureType distinguishes an unspecified failure type from one
	// explicitly reported as 0x00, which means "no sub-type information".
	HasFailureType bool

	// StatusMask is the raw UDS statusOfDTC byte. Zero for OBD-II codes,
	// which carry their status in which mode answered instead.
	StatusMask byte
}

// FullCode renders the code as a technician would read it, with the failure
// type appended when the ECU supplied one.
func (d DTC) FullCode() string {
	if !d.HasFailureType {
		return d.Code
	}
	return fmt.Sprintf("%s-%02X", d.Code, d.FailureType)
}

// failureTypes are the ISO 14229-1 failure type bytes worth naming. The full
// list is long and much of it is rarely seen; an unlisted value is reported as
// its hex rather than guessed at.
var failureTypes = map[byte]string{
	0x00: "no sub-type information",
	0x11: "circuit short to ground",
	0x12: "circuit short to battery",
	0x13: "circuit open",
	0x14: "circuit short to ground or open",
	0x15: "circuit short to battery or open",
	0x16: "circuit voltage below threshold",
	0x17: "circuit voltage above threshold",
	0x1C: "circuit voltage out of range",
	0x21: "signal amplitude below minimum",
	0x22: "signal amplitude above maximum",
	0x29: "signal invalid",
	0x2F: "signal erratic",
	0x31: "no signal",
	0x38: "component operating conditions not met",
	0x42: "general checksum failure",
	0x49: "internal electronic failure",
	0x4B: "over temperature",
	0x54: "missing calibration",
	0x55: "not programmed",
	0x62: "signal compare failure",
	0x64: "signal plausibility failure",
	0x68: "event information",
	0x73: "actuator stuck",
	0x81: "invalid serial data received",
	0x87: "missing message",
	0x92: "performance or incorrect operation",
	0x96: "component internal failure",
}

// FailureTypeName describes how the component failed.
//
// It is empty when the ECU reported no failure type, and also when it reported
// 0x00, which means it has no sub-type detail to give: printing "no sub-type
// information" beside a description tells the reader nothing.
func (d DTC) FailureTypeName() string {
	if !d.HasFailureType || d.FailureType == 0x00 {
		return ""
	}
	if name, ok := failureTypes[d.FailureType]; ok {
		return name
	}
	return fmt.Sprintf("failure type %02X", d.FailureType)
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

func (d DTC) String() string { return d.FullCode() }

// UDS statusOfDTC bits, from ISO 14229-1. A code carries these instead of the
// mode that reported it, so the same byte says whether a fault is confirmed,
// merely pending, and whether it is currently illuminating a warning lamp.
const (
	StatusTestFailed           byte = 1 << 0
	StatusTestFailedThisCycle  byte = 1 << 1
	StatusPending              byte = 1 << 2
	StatusConfirmed            byte = 1 << 3
	StatusTestNotCompleted     byte = 1 << 4
	StatusTestFailedSinceClear byte = 1 << 5
	StatusTestIncompleteCycle  byte = 1 << 6
	StatusWarningRequested     byte = 1 << 7
)

// DecodeUDS turns the three bytes of a UDS trouble code and its status byte
// into a DTC.
//
// The first two bytes use the same J2012 encoding as OBD-II, so the code text
// is shared; the third says how the component failed. The status byte replaces
// the OBD-II convention of inferring severity from which mode answered.
func DecodeUDS(a, b, failureType, status byte) DTC {
	d := Decode(a, b)
	d.FailureType = failureType
	d.HasFailureType = true
	d.StatusMask = status
	d.Status = statusFromMask(status)
	return d
}

// statusFromMask maps the UDS status bits onto the three states the rest of
// the tool speaks in.
//
// There is no UDS equivalent of an OBD-II permanent code: permanence is an
// emissions-regulation concept, so a UDS code is only ever stored or pending.
func statusFromMask(mask byte) Status {
	switch {
	case mask&StatusConfirmed != 0:
		return Stored
	case mask&StatusPending != 0, mask&StatusTestFailed != 0:
		return Pending
	default:
		return Stored
	}
}

// WarningActive reports whether the ECU is asking for a warning lamp, which is
// the closest UDS gets to "this one matters now".
func (d DTC) WarningActive() bool {
	return d.StatusMask&StatusWarningRequested != 0
}
