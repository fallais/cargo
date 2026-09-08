package displayer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The adapter page is where connecting happens. Attaching to a car is not
// something to do behind the user's back, so it is an explicit act by default
// and autoconnect is opt-in.
//
// The page is one centred card rather than a list beside a detail pane. There
// is exactly one decision to make here - which adapter, then connect - and a
// full-width two-column layout made a two-field form look like a file browser.

// Card geometry. The width is fixed rather than proportional: the card holds
// one short form, and stretching it across a wide terminal would make two
// fields look like a file browser. The height is measured, not guessed - see
// formRows.
const (
	adapterCardWidth = 60
	// adapterItemPadding is the blank line between form rows.
	adapterItemPadding = 1
	// adapterInfoRows is the status block under the form, and adapterHintRows
	// the key reminder under that.
	adapterInfoRows = 3
	adapterHintRows = 1
)

// formSlack is spare rows added to the measured form height.
//
// Getting this wrong is silent and nasty: a form given less room than it needs
// scrolls to keep the focused item visible, so tabbing to a button pushed the
// adapter picker off the top of the card and it simply vanished. tview's
// vertical layout does not lay out quite the way its arithmetic reads, and a
// couple of spare rows in a centred card cost nothing, so this errs high
// rather than trying to predict it exactly.
const formSlack = 2

// formRows is how many rows a vertical form needs, derived the way tview lays
// one out: each item's own field height, the padding between them, then a row
// for the buttons.
func formRows(f *tview.Form, padding int) int {
	rows := 0
	for i := 0; i < f.GetFormItemCount(); i++ {
		height := f.GetFormItem(i).GetFieldHeight()
		if height <= 0 {
			// tview falls back to DefaultFormFieldHeight, which is 5.
			height = tview.DefaultFormFieldHeight
		}
		rows += height + padding
	}
	if f.GetButtonCount() > 0 {
		if padding == 0 {
			rows++ // tview inserts a blank line before the buttons
		}
		rows++
	}
	return rows
}

func (d *Displayer) buildAdapter() tview.Primitive {
	d.adapterDrop = tview.NewDropDown().SetLabel(blurredLabel("Adapter"))
	// Keep the open list from running off the card on a short terminal.
	d.adapterDrop.SetFieldWidth(38)
	markFocus(d.adapterDrop.Box, "Adapter", func(label string) {
		d.adapterDrop.SetLabel(label)
	})

	// The open list is drawn by the dropdown itself rather than the form,
	// so it needs the palette applied here or it keeps tview's defaults.
	d.adapterDrop.SetListStyles(
		tcell.StyleDefault.Background(theme.background).Foreground(theme.text),
		tcell.StyleDefault.Background(theme.title).Foreground(tcell.ColorBlack))

	d.autoCheck = tview.NewCheckbox().SetLabel(blurredLabel("Autoconnect"))
	markFocus(d.autoCheck.Box, "Autoconnect", func(label string) {
		d.autoCheck.SetLabel(label)
	})
	d.autoCheck.SetChangedFunc(func(on bool) {
		if on == d.provider.Autoconnect() {
			return
		}
		d.setAutoconnect(on)
	})

	d.adapterForm = tview.NewForm().
		SetButtonsAlign(tview.AlignCenter).
		SetItemPadding(adapterItemPadding)

	// Grey until focused, then filled amber. A terminal has no pointer, so
	// which button Enter would press has to be obvious from colour alone.
	// Black on amber rather than the theme background: the terminal theme
	// leaves the background as the terminal's own, and using it as a
	// foreground here would be unreadable against the fill.
	d.adapterForm.
		SetButtonStyle(tcell.StyleDefault.
			Background(theme.background).
			Foreground(theme.dim)).
		SetButtonActivatedStyle(tcell.StyleDefault.
			Background(theme.title).
			Foreground(tcell.ColorBlack).
			Bold(true))

	// Fields paint nothing of their own. tview fills them with
	// ContrastBackgroundColor by default, which put a grey slab behind the
	// picker in the theme whose whole point is to leave the terminal's own
	// background alone. Form.Draw pushes these onto every item on each
	// frame, so they have to be set here and not on the items.
	d.adapterForm.
		SetFieldBackgroundColor(theme.background).
		SetFieldTextColor(theme.text).
		SetLabelColor(theme.dim)
	d.adapterForm.AddFormItem(d.adapterDrop)
	d.adapterForm.AddFormItem(d.autoCheck)
	d.adapterForm.AddButton("Connect", d.connectSelected)
	d.adapterForm.AddButton("Disconnect", d.disconnect)
	d.adapterForm.AddButton("Rescan", d.rescanAdapters)

	d.adapterInfo = tview.NewTextView().SetDynamicColors(true)

	hint := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[gray]tab[white] move   [gray]enter[white] choose or press   [gray]space[white] toggle")

	formHeight := formRows(d.adapterForm, adapterItemPadding) + formSlack

	card := tview.NewFlex().SetDirection(tview.FlexRow)
	card.SetBorder(true).SetTitle(" Connect to a vehicle ")
	card.AddItem(d.adapterForm, formHeight, 0, true)
	card.AddItem(d.adapterInfo, adapterInfoRows, 0, false)
	card.AddItem(hint, adapterHintRows, 0, false)

	// Two rows for the border.
	cardHeight := formHeight + adapterInfoRows + adapterHintRows + 2

	// Centre the card on both axes by padding it with empty weighted cells.
	row := tview.NewFlex()
	row.AddItem(spacer(), 0, 1, false)
	row.AddItem(card, adapterCardWidth, 0, true)
	row.AddItem(spacer(), 0, 1, false)

	page := tview.NewFlex().SetDirection(tview.FlexRow)
	page.AddItem(spacer(), 0, 1, false)
	page.AddItem(row, cardHeight, 0, true)
	page.AddItem(spacer(), 0, 1, false)

	d.refreshAdapterList()
	return page
}

// refreshAdapterList rescans for devices and repopulates the picker. It runs
// on the UI goroutine.
func (d *Displayer) refreshAdapterList() {
	_, previous := d.adapterDrop.GetCurrentOption()
	d.adapters = d.provider.Adapters()

	options := make([]string, 0, len(d.adapters))
	selected := 0
	for i, a := range d.adapters {
		label := a.Port
		if a.Detail != "" {
			label += "  (" + a.Detail + ")"
		}
		if a.Connected {
			label = "* " + label
		}
		options = append(options, label)

		// Keep the user's choice across a rescan, and otherwise open on
		// whatever is already connected.
		if label == previous || (previous == "" && a.Connected) {
			selected = i
		}
	}

	if len(options) == 0 {
		options = []string{"no adapters found"}
	}

	// Set the options with no callback: a selection made here is the list
	// being rebuilt, not the user choosing anything.
	d.adapterDrop.SetOptions(options, nil)
	d.adapterDrop.SetCurrentOption(selected)

	if d.autoCheck.IsChecked() != d.provider.Autoconnect() {
		d.autoCheck.SetChecked(d.provider.Autoconnect())
	}
	d.paintAdapterInfo()
}

func (d *Displayer) paintAdapterInfo() {
	status := "[red]●[white] not connected"
	if d.provider.IsConnected() {
		status = "[green]●[white] connected"
	}

	out := fmt.Sprintf("  %s\n", status)
	out += fmt.Sprintf("  [gray]%s[white]", d.provider.Description())

	if !d.provider.IsConnected() {
		out += fmt.Sprintf("   [gray]%d device(s) found[white]", len(d.adapters))
	}
	d.adapterInfo.SetText(out)
}

// selectedPort returns the port the picker is showing, or "" when there is
// nothing to connect to.
func (d *Displayer) selectedPort() string {
	index, _ := d.adapterDrop.GetCurrentOption()
	if index < 0 || index >= len(d.adapters) {
		return ""
	}
	return d.adapters[index].Port
}

func (d *Displayer) connectSelected() {
	port := d.selectedPort()
	if port == "" {
		d.flash("[yellow]No adapter to connect to. Press Rescan.")
		return
	}
	d.connectTo(port)
}

func (d *Displayer) rescanAdapters() {
	d.refreshAdapterList()
	d.flash(fmt.Sprintf("Scanned: %d device(s)", len(d.adapters)))
}

// connectTo attaches to one device, off the UI goroutine so a slow probe does
// not freeze the screen.
func (d *Displayer) connectTo(port string) {
	d.flash("Connecting to " + port + "...")

	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, 60*time.Second)
		defer cancel()

		err := d.provider.Connect(ctx, port)
		d.app.QueueUpdateDraw(func() {
			if err != nil {
				d.flash("[red]" + port + ": " + err.Error())
				d.refreshAdapterList()
				return
			}
			d.connectedPort = port
			d.flash("[green]Connected to " + port)
			d.refreshAdapterList()
		})

		if err == nil {
			d.refreshLive()
			d.refreshDTCs()
		}
	}()
}

func (d *Displayer) disconnect() {
	d.provider.Disconnect()
	d.connectedPort = ""
	d.refreshAdapterList()
	d.flash("Disconnected")
}

func (d *Displayer) setAutoconnect(on bool) {
	d.provider.SetAutoconnect(on)
	d.paintAdapterInfo()

	if on {
		d.flash("[green]Autoconnect on: will attach to the first adapter that answers")
		return
	}
	d.flash("Autoconnect off")
}

func (d *Displayer) onAdapterKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'x', 'X':
		d.disconnect()
		return nil
	case 's', 'S':
		d.rescanAdapters()
		return nil
	case 't', 'T':
		d.setAutoconnect(!d.provider.Autoconnect())
		d.autoCheck.SetChecked(d.provider.Autoconnect())
		return nil
	}
	return event
}

// watchConnection keeps the adapter page honest when autoconnect attaches or a
// cable is pulled without the user touching anything.
func (d *Displayer) watchConnection() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	was := d.provider.IsConnected()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			now := d.provider.IsConnected()
			if now == was {
				continue
			}
			was = now
			slog.Debug("Connection state changed", "connected", now)
			d.app.QueueUpdateDraw(d.refreshAdapterList)
		}
	}
}
