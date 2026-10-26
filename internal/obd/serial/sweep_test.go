package serial

import (
	"context"
	"testing"

	"github.com/fallais/cargo/internal/obd"
)

func TestSweepFindsModulesAtEveryAddress(t *testing.T) {
	s, line := newScriptedProvider(t, nil)
	// A module answers only a request addressed to it, and only an
	// adapter listening where it replies hears it. Everywhere else the
	// sweep sees what an empty address gives: nothing.
	type pair struct{ req, resp uint16 }
	modules := map[pair]string{
		{0x740, 0x760}: "7F2212",
		{0x745, 0x765}: "7F2212",
		{0x7E0, 0x7E8}: "014\r0:62F190555531",
		{0x7F1, 0x7F9}: "7F2212",
	}
	var addressed, listening uint16
	line.onCommand = func(cmd string) (string, bool) {
		var id uint16
		if _, err := fmtSscanf(cmd, "ATSH%03X", &id); err == nil {
			addressed = id
			return "OK", true
		}
		if _, err := fmtSscanf(cmd, "ATCRA%03X", &id); err == nil {
			listening = id
			return "OK", true
		}
		if cmd == "22F190" {
			if r, ok := modules[pair{addressed, listening}]; ok {
				return r, true
			}
			return "NO DATA", true
		}
		return "", false
	}

	var last obd.ScanProgress
	found, err := s.SweepModules(context.Background(), func(p obd.ScanProgress) { last = p })
	if err != nil {
		t.Fatalf("SweepModules: %v", err)
	}

	want := []struct {
		name      string
		req, resp uint16
		standard  bool
	}{
		{"Airbag", 0x740, 0x760, false},
		{"Body Control", 0x745, 0x765, false},
		{"Engine", 0x7E0, 0x7E8, true},
		{"Module 7F1", 0x7F1, 0x7F9, false},
	}
	if len(found) != len(want) {
		t.Fatalf("found %d modules, want %d: %+v", len(found), len(want), found)
	}
	for i, w := range want {
		m := found[i]
		if m.Name != w.name || m.Request != w.req || m.Response != w.resp || m.Standard != w.standard {
			t.Errorf("module %d = %+v, want %+v", i, m, w)
		}
	}
	if !last.Done || last.Answered != 4 {
		t.Errorf("last progress = %+v, want done with 4 answered", last)
	}

	// The next scan reads exactly what the sweep found.
	if got := s.Modules(); len(got) != 4 {
		t.Errorf("module list after the sweep has %d entries, want 4", len(got))
	}
}

func TestSweepAddressesSkipsRepliesAndBroadcast(t *testing.T) {
	for _, id := range sweepAddresses() {
		if id == obd.Functional || (id >= 0x7E8 && id <= 0x7EF) {
			t.Errorf("sweep asks on %03X", id)
		}
	}
	if n := len(sweepAddresses()); n != 0x100-1-8 {
		t.Errorf("sweep asks on %d addresses, want %d", n, 0x100-1-8)
	}
}

func TestParseIdentifier(t *testing.T) {
	for _, tc := range []struct {
		response, did, want string
	}{
		{"00D\r0:62F187323438\r1:31303634353952", "F187", "248106459R"},
		{"043\r0:62F18A526F62\r1:65727420426F73\r2:636820476D6248\r3:2C205641542049\r4:44204E6F204445\r5:38313131323831\r6:33352C20537570\r7:706C696572204E\r8:6F203033372020\r9:2020202020AAAA",
			"F18A", "Robert Bosch GmbH, VAT ID No DE811128135, Supplier No 037"},
		{"017\r0:62F19745434D\r1:002D456E67696E\r2:65436F6E74726F\r3:6C0000AAAAAAAA", "F197", "ECM -EngineControl"},
	} {
		got, err := ParseIdentifier(tc.response, tc.did)
		if err != nil || got != tc.want {
			t.Errorf("ParseIdentifier(%s) = %q, %v; want %q", tc.did, got, err, tc.want)
		}
	}
	if _, err := ParseIdentifier("7F2212", "F187"); err == nil {
		t.Error("a refusal was read as an identifier")
	}
	if _, err := ParseIdentifier("62F190555531", "F187"); err == nil {
		t.Error("the reply to another identifier was accepted")
	}
}

func TestParseKWPIdentification(t *testing.T) {
	got, err := ParseKWPIdentification("01A\r0:618035383031\r1:52093033373734\r2:34375241640466\r3:05010201008800")
	if err != nil {
		t.Fatalf("ParseKWPIdentification: %v", err)
	}
	if want := "5801R 0377447RAd"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSupplierName(t *testing.T) {
	if got := supplierName("Robert Bosch GmbH, VAT ID No DE811128135"); got != "Robert Bosch GmbH" {
		t.Errorf("got %q", got)
	}
}
