package serial

import (
	"errors"
	"testing"

	"github.com/fallais/cargo/internal/dtc"
)

// Byte-accurate cases. A UDS record is three code bytes plus a status byte.
func TestParseUDSDTCsRecords(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		codes       []string
		full        []string
		statuses    []dtc.Status
		failureName string
	}{
		{
			// C0035 (left front wheel speed sensor), failure type 64
			// (signal plausibility), status 09 = confirmed + testFailed.
			name:     "one confirmed code",
			in:       "5902FF" + "403564" + "09",
			codes:    []string{"C0035"},
			full:     []string{"C0035-64"},
			statuses: []dtc.Status{dtc.Stored},
		},
		{
			name:     "two codes",
			in:       "5902FF" + "403564" + "09" + "812311" + "04",
			codes:    []string{"C0035", "B0123"},
			full:     []string{"C0035-64", "B0123-11"},
			statuses: []dtc.Status{dtc.Stored, dtc.Pending},
		},
		{
			name:     "padding is skipped",
			in:       "5902FF" + "403564" + "09" + "00000000",
			codes:    []string{"C0035"},
			full:     []string{"C0035-64"},
			statuses: []dtc.Status{dtc.Stored},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseUDSDTCs(tc.in)
			if err != nil {
				t.Fatalf("ParseUDSDTCs(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.codes) {
				t.Fatalf("got %d codes, want %d: %+v", len(got), len(tc.codes), got)
			}
			for i := range tc.codes {
				if got[i].Code != tc.codes[i] {
					t.Errorf("code %d = %q, want %q", i, got[i].Code, tc.codes[i])
				}
				if got[i].FullCode() != tc.full[i] {
					t.Errorf("full code %d = %q, want %q", i, got[i].FullCode(), tc.full[i])
				}
				if got[i].Status != tc.statuses[i] {
					t.Errorf("status %d = %v, want %v", i, got[i].Status, tc.statuses[i])
				}
				if !got[i].HasFailureType {
					t.Errorf("code %d lost its failure type", i)
				}
			}
		})
	}
}

// The failure type is what makes a UDS code actionable: it says how the
// component failed, not just which one.
func TestFailureTypeNaming(t *testing.T) {
	codes, err := ParseUDSDTCs("5902FF" + "403564" + "09")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codes[0].FailureTypeName(), "signal plausibility failure"; got != want {
		t.Errorf("failure type = %q, want %q", got, want)
	}

	// 0x00 means the ECU has no sub-type detail, so there is nothing worth
	// printing alongside the description.
	codes, err = ParseUDSDTCs("5902FF" + "403500" + "09")
	if err != nil {
		t.Fatal(err)
	}
	if got := codes[0].FailureTypeName(); got != "" {
		t.Errorf("failure type 00 named %q, want it suppressed", got)
	}
	if got, want := codes[0].FullCode(), "C0035-00"; got != want {
		t.Errorf("full code = %q, want %q", got, want)
	}

	// An unlisted failure type is reported by number rather than guessed at.
	codes, err = ParseUDSDTCs("5902FF" + "4035AB" + "09")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codes[0].FailureTypeName(), "failure type AB"; got != want {
		t.Errorf("unknown failure type = %q, want %q", got, want)
	}
}

// The status byte carries the severity that OBD-II infers from which mode
// answered, so it has to be read rather than assumed.
func TestUDSStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status byte
		want   dtc.Status
	}{
		{dtc.StatusConfirmed, dtc.Stored},
		{dtc.StatusConfirmed | dtc.StatusTestFailed, dtc.Stored},
		{dtc.StatusPending, dtc.Pending},
		{dtc.StatusTestFailed, dtc.Pending},
	} {
		d := dtc.DecodeUDS(0x40, 0x35, 0x64, tc.status)
		if d.Status != tc.want {
			t.Errorf("status %02X mapped to %v, want %v", tc.status, d.Status, tc.want)
		}
	}

	warning := dtc.DecodeUDS(0x40, 0x35, 0x64, dtc.StatusConfirmed|dtc.StatusWarningRequested)
	if !warning.WarningActive() {
		t.Error("warning bit was lost")
	}
}

// A module that refuses has to be told apart from one that is absent, because
// the first is worth reporting and the second is normal on a scan across makes.
func TestUDSNegativeResponses(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want error
	}{
		{"7F 19 11", ErrUDSNotSupported},
		{"7F 19 12", ErrUDSNotSupported},
		{"7F 19 22", ErrUDSConditions},
		{"7F 19 33", ErrUDSSecurity},
		{"7F 19 31", ErrUDSOutOfRange},
		{"7F 19 21", ErrUDSBusy},
		{"7F 19 7F", ErrUDSWrongSession},
		{"7F 19 80", ErrUDSWrongSession},
	} {
		if _, err := ParseUDSDTCs(tc.in); !errors.Is(err, tc.want) {
			t.Errorf("ParseUDSDTCs(%q) err = %v, want %v", tc.in, err, tc.want)
		}
	}
}

// An ECU may say "still working" one or more times before the real answer, and
// both can arrive in a single read. The pending frames must be stepped over,
// not mistaken for a failure.
func TestUDSResponsePendingThenAnswer(t *testing.T) {
	in := "7F1978" + "7F1978" + "5902FF" + "403564" + "09"

	codes, err := ParseUDSDTCs(in)
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}
	if len(codes) != 1 || codes[0].Code != "C0035" {
		t.Errorf("got %+v, want the code that followed the pending frames", codes)
	}

	if !isResponsePending("7F1978") {
		t.Error("a bare pending frame was not recognised")
	}
	if isResponsePending("5902FF40356409") {
		t.Error("a positive response was mistaken for pending")
	}
}

// Only pending frames and no answer must not look like success.
func TestUDSOnlyPending(t *testing.T) {
	if _, err := ParseUDSDTCs("7F1978"); err == nil {
		t.Error("a reply of nothing but pending frames was accepted")
	}
}

// An empty fault list is the healthy case and must parse cleanly.
func TestUDSNoCodes(t *testing.T) {
	codes, err := ParseUDSDTCs("5902FF")
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}
	if len(codes) != 0 {
		t.Errorf("got %+v, want no codes", codes)
	}
}

// Multi-frame replies are the normal case for a module with several faults.
func TestUDSMultiFrame(t *testing.T) {
	in := "00B\r0: 59 02 FF 40 35 64\r1: 09 81 23 11 04\r"

	codes, err := ParseUDSDTCs(in)
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("got %d codes, want 2: %+v", len(codes), codes)
	}
	if codes[0].FullCode() != "C0035-64" || codes[1].FullCode() != "B0123-11" {
		t.Errorf("got %s and %s", codes[0].FullCode(), codes[1].FullCode())
	}
}

// Matching the service echo alone would collide with data; the sub-function
// has to match too.
func TestUDSPayloadNeedsSubFunction(t *testing.T) {
	if _, err := udsPayload([]byte{0x59, 0x01, 0xFF, 0x00}, 0x19, 0x02); err == nil {
		t.Error("a reply to a different sub-function was accepted")
	}
}

// A module asked with a 0xFF mask reports every code it holds a slot for. Only
// the records whose status says the test actually failed are faults; the rest
// describe the test, not the component, and one airbag module returns forty of
// them.
func TestParseUDSDTCsSkipsRecordsThatAreNotFaults(t *testing.T) {
	// Two "not tested this cycle" records (0x40) around one confirmed
	// fault (0x28), as read from a Dacia airbag module.
	codes, err := ParseUDSDTCs("59027B402004404020 1C40C422002800000000")
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}
	if len(codes) != 1 {
		t.Fatalf("got %d codes, want 1: %+v", len(codes), codes)
	}
	if got, want := codes[0].FullCode(), "U0422-00"; got != want {
		t.Errorf("code = %s, want %s", got, want)
	}
	if got, want := codes[0].StatusMask, byte(0x28); got != want {
		t.Errorf("status = %02X, want %02X", got, want)
	}
}

func TestParseUDSDTCsKeepsWarningRecords(t *testing.T) {
	// Status A8: confirmed, failed since clear, warning lamp requested.
	codes, err := ParseUDSDTCs("5902B9956023A8")
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}
	if len(codes) != 1 {
		t.Fatalf("got %d codes, want 1", len(codes))
	}
	if !codes[0].WarningActive() {
		t.Error("warning indicator should be active for status A8")
	}
}

func TestParseUDSExtendedData(t *testing.T) {
	// 59 06, code C1A60-7B, status A8, then record 01 holding 0C 03 00.
	records, err := ParseUDSExtendedData("59065A607BA8010C0300")
	if err != nil {
		t.Fatalf("ParseUDSExtendedData: %v", err)
	}
	if got, want := hexString(records), "010C0300"; got != want {
		t.Errorf("records = %s, want %s", got, want)
	}
}

// A module asked for a record it does not keep answers with the header alone.
func TestParseUDSExtendedDataNoRecord(t *testing.T) {
	records, err := ParseUDSExtendedData("590608332928")
	if err != nil {
		t.Fatalf("ParseUDSExtendedData: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("records = % X, want none", records)
	}
}

func TestParseUDSExtendedDataRejectsTruncated(t *testing.T) {
	for _, in := range []string{"59065A607B", "59065A607BA801", "7F1911"} {
		if records, err := ParseUDSExtendedData(in); err == nil {
			t.Errorf("ParseUDSExtendedData(%q) = % X, want an error", in, records)
		}
	}
}

func TestParseUDSSnapshot(t *testing.T) {
	// 59 04, code, status, record 01, two identifiers, then their payload.
	identifiers, raw, err := ParseUDSSnapshot("59045A607BA80102F1900011")
	if err != nil {
		t.Fatalf("ParseUDSSnapshot: %v", err)
	}
	if identifiers != 2 {
		t.Errorf("identifiers = %d, want 2", identifiers)
	}
	if len(raw) == 0 {
		t.Error("snapshot payload should be carried through raw")
	}
}

// Replies read from a 2015 Dacia Sandero, kept whole: they exercise the length
// line, FF padding, ordinal wrap and a pending reply ahead of the answer at
// once, which is the combination that hid faults on the real car.
const (
	sanderoBodyControl = "087\r0:5902B9950414\r1:10951014109511\r2:14109512141095\r" +
		"3:13141095141410\r4:95201210952014\r5:10955012109550\r6:14109571011095\r" +
		"7:71151095711110\r8:95706810953018\r9:28953118289560\rA:23A8956123A895\r" +
		"B:62231095622410\rC:95A06428956804\rD:105A607BA95A82\rE:68A95A8368A95A\r" +
		"F:727AA85A737A10\r0:5A757AA95A861C\r1:105A8655105A86\r2:96109505011095\r" +
		"3:722910FFFFFFFF"
	sanderoEngine = "7F1978\r073\r0:5902FF038013\r1:40038011400380\r2:12402263215022\r" +
		"3:63225001902440\r4:083329281525F3\r5:28056429280380\r6:96600480124020\r" +
		"7:02944011301C40\r8:06451240040A17\r9:4011351C600101\rA:22401489174014\r" +
		"B:8E224000956440\rC:025A1340025A11\rD:40158964281515\rE:73501515725004\r" +
		"F:00F540158A2950\r0:15892928AAAAAA"
)

func TestParseUDSDTCsSanderoBodyControl(t *testing.T) {
	codes, err := ParseUDSDTCs(sanderoBodyControl)
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}

	want := []string{"B1530-18", "B1531-18", "B1560-23", "B1561-23", "B15A0-64",
		"C1A60-7B", "C1A82-68", "C1A83-68", "C1A72-7A", "C1A75-7A"}
	if len(codes) != len(want) {
		t.Fatalf("got %d codes, want %d: %v", len(codes), len(want), codes)
	}
	for i, c := range codes {
		if c.FullCode() != want[i] {
			t.Errorf("code %d = %s, want %s", i, c.FullCode(), want[i])
		}
	}

	// C1A60 is the live one, and its module wants the lamp lit.
	if c := codes[5]; !c.FailingNow() || !c.WarningActive() {
		t.Errorf("%s: failing now %v, lamp %v; want both", c.FullCode(), c.FailingNow(), c.WarningActive())
	}
	// B1530 is confirmed history: neither.
	if c := codes[0]; c.FailingNow() || c.WarningActive() {
		t.Errorf("%s: failing now %v, lamp %v; want neither", c.FullCode(), c.FailingNow(), c.WarningActive())
	}
}

func TestParseUDSDTCsSanderoEngine(t *testing.T) {
	codes, err := ParseUDSDTCs(sanderoEngine)
	if err != nil {
		t.Fatalf("ParseUDSDTCs: %v", err)
	}

	want := []string{"P0833-29", "P1525-F3", "P0564-29", "P0380-96", "P1135-1C", "P1589-64", "P1589-29"}
	if len(codes) != len(want) {
		t.Fatalf("got %d codes, want %d: %v", len(codes), len(want), codes)
	}
	for i, c := range codes {
		if c.FullCode() != want[i] {
			t.Errorf("code %d = %s, want %s", i, c.FullCode(), want[i])
		}
	}
}
