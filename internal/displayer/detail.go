package displayer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/rivo/tview"
)

// The detail card answers the question a fault list cannot: is this happening,
// or did it happen? A status byte says a code is stored; the occurrence
// counter says whether it has been stored once or two hundred times, which is
// the difference between a fault worth chasing and a fault worth clearing.

const detailPage = "dtc-detail"

const (
	detailCardWidth  = 72
	detailCardHeight = 18
)

// detailTimeout bounds the two round trips the card needs.
const detailTimeout = 15 * time.Second

func (d *Displayer) buildDetail() tview.Primitive {
	d.detailText = tview.NewTextView().SetDynamicColors(true).SetWrap(true)

	card := tview.NewFlex().SetDirection(tview.FlexRow)
	card.SetBorder(true).SetTitle(" Code detail ")
	card.AddItem(d.detailText, 0, 1, false)
	card.AddItem(tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[gray]esc[white] back"), 1, 0, false)

	row := tview.NewFlex()
	row.AddItem(spacer(), 0, 1, false)
	row.AddItem(card, detailCardWidth, 0, true)
	row.AddItem(spacer(), 0, 1, false)

	page := tview.NewFlex().SetDirection(tview.FlexRow)
	page.AddItem(spacer(), 0, 1, false)
	page.AddItem(row, detailCardHeight, 0, true)
	page.AddItem(spacer(), 0, 1, false)
	return page
}

// showDetail opens the card for the selected row and fills it in off the UI
// goroutine, because it costs two serial round trips.
func (d *Displayer) showDetail() {
	code, ok := d.selectedCode()
	if !ok {
		return
	}

	d.detailText.SetText("\n  Reading " + code.FullCode() + " from " + code.Module + " ...")
	d.pages.SwitchToPage(detailPage)

	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, detailTimeout)
		defer cancel()

		detail, err := d.provider.DTCDetail(ctx, code)
		d.app.QueueUpdateDraw(func() {
			d.detailText.SetText(renderDetail(code, d.resolver.Describe(code), detail, err))
		})
	}()
}

// selectedCode maps the highlighted table row back to the code it shows.
func (d *Displayer) selectedCode() (dtc.DTC, bool) {
	row, _ := d.dtcTable.GetSelection()
	// Row 0 is the header, and the table is drawn from d.shown in order.
	index := row - 1
	if index < 0 || index >= len(d.shown) {
		return dtc.DTC{}, false
	}
	return d.shown[index], true
}

func renderDetail(code dtc.DTC, def dtc.Definition, detail obd.DTCDetail, err error) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n  [::b]%s[::-]  %s\n", code.FullCode(), code.Module)
	fmt.Fprintf(&b, "  [gray]%s[white]\n\n", def.Description)

	fmt.Fprintf(&b, "  Status byte   [::b]%02X[::-]   %s\n", code.StatusMask, statusWords(code.StatusMask))

	if err != nil {
		fmt.Fprintf(&b, "\n  [yellow]No detail: %v[white]\n", err)
		return b.String()
	}

	if detail.Occurrences >= 0 {
		fmt.Fprintf(&b, "  Occurrences   [::b]%d[::-]\n", detail.Occurrences)
	} else {
		b.WriteString("  Occurrences   [gray]not reported by this module[white]\n")
	}

	// The bytes past the counter are the manufacturer's to define, so they
	// are shown as they arrived rather than decoded into invented fields.
	if len(detail.Extended) > 0 {
		fmt.Fprintf(&b, "  Extended      [gray]%s[white]\n", hexWords(detail.Extended))
	}

	if detail.SnapshotIdentifiers > 0 {
		fmt.Fprintf(&b, "\n  Freeze frame  [::b]%d[::-] recorded value(s) at the moment it set\n",
			detail.SnapshotIdentifiers)
		fmt.Fprintf(&b, "  [gray]%s[white]\n", hexWords(detail.Snapshot))
		b.WriteString("\n  [gray]The identifiers are manufacturer-defined, so the bytes\n" +
			"  are shown raw rather than guessed at.[white]\n")
	} else {
		b.WriteString("\n  Freeze frame  [gray]none stored[white]\n")
	}

	return b.String()
}

// statusWords spells out the UDS status bits that decide whether a fault is
// live or history.
func statusWords(mask byte) string {
	if mask == 0 {
		return "[gray]not a UDS code[white]"
	}

	var on []string
	for _, bit := range []struct {
		mask byte
		name string
	}{
		{dtc.StatusTestFailed, "[red]failing now[white]"},
		{dtc.StatusTestFailedThisCycle, "failed this cycle"},
		{dtc.StatusPending, "pending"},
		{dtc.StatusConfirmed, "confirmed"},
		{dtc.StatusTestNotCompleted, "not completed since clear"},
		{dtc.StatusTestFailedSinceClear, "failed since clear"},
		{dtc.StatusTestIncompleteCycle, "not completed this cycle"},
		{dtc.StatusWarningRequested, "[yellow]warning lamp[white]"},
	} {
		if mask&bit.mask != 0 {
			on = append(on, bit.name)
		}
	}
	return strings.Join(on, ", ")
}

// hexWords renders bytes in space-separated pairs, wrapped by the text view.
func hexWords(b []byte) string {
	parts := make([]string, 0, len(b))
	for _, v := range b {
		parts = append(parts, fmt.Sprintf("%02X", v))
	}
	return strings.Join(parts, " ")
}
