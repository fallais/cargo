package app

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/fallais/cargo/internal/dtc"
)

func codesFrom(pairs ...string) []dtc.DTC {
	var codes []dtc.DTC
	for i := 0; i < len(pairs); i += 2 {
		codes = append(codes, dtc.DTC{Module: pairs[i], Code: pairs[i+1]})
	}
	return codes
}

func TestSelectCodesDefaultsToEveryModuleWithCodes(t *testing.T) {
	codes := codesFrom("Engine", "P0380", "Body Control", "C1A60", "Engine", "P0833")

	selected, modules, err := selectCodes(codes, nil)
	if err != nil {
		t.Fatalf("selectCodes: %v", err)
	}
	if len(selected) != 3 {
		t.Errorf("selected %d codes, want 3", len(selected))
	}
	if want := []string{"Body Control", "Engine"}; !reflect.DeepEqual(modules, want) {
		t.Errorf("modules = %v, want %v", modules, want)
	}
}

func TestSelectCodesNarrowsToNamedModules(t *testing.T) {
	codes := codesFrom("Engine", "P0380", "Body Control", "C1A60")

	selected, modules, err := selectCodes(codes, []string{"body control"})
	if err != nil {
		t.Fatalf("selectCodes: %v", err)
	}
	if len(selected) != 1 || selected[0].Code != "C1A60" {
		t.Errorf("selected %v, want only C1A60", selected)
	}
	if want := []string{"Body Control"}; !reflect.DeepEqual(modules, want) {
		t.Errorf("modules = %v, want %v", modules, want)
	}
}

// A typo must not quietly clear nothing and look like success.
func TestSelectCodesRejectsUnknownModule(t *testing.T) {
	codes := codesFrom("Engine", "P0380")

	_, _, err := selectCodes(codes, []string{"Body"})
	if !errors.Is(err, errNoSuchModule) {
		t.Fatalf("err = %v, want errNoSuchModule", err)
	}
	if !strings.Contains(err.Error(), `"Engine"`) {
		t.Errorf("error %q does not say which modules did report", err)
	}
}

func TestConfirm(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"YES\n", true},
		{" yes \n", true},
		{"\n", false},
		{"n\n", false},
		{"maybe\n", false},
		{"y", true}, // no trailing newline, as from a pipe
		{"", false}, // no terminal at all
	} {
		if got := confirm(strings.NewReader(tc.in), io.Discard, "Clear?"); got != tc.want {
			t.Errorf("confirm(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
