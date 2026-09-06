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

func (d *Displayer) buildAdapter() tview.Primitive {
	d.adapterList = tview.NewList().ShowSecondaryText(true)
	d.adapterList.SetBorder(true).SetTitle(" Adapters ")
	d.adapterList.SetChangedFunc(func(int, string, string, rune) { d.paintAdapterInfo() })

	d.adapterInfo = tview.NewTextView().SetDynamicColors(true)
	d.adapterInfo.SetBorder(true).SetTitle(" Connection ")

	help := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[::b]enter[::-] connect   [::b]x[::-] disconnect   " +
			"[::b]s[::-] scan again   [::b]t[::-] autoconnect")

	right := tview.NewFlex().SetDirection(tview.FlexRow)
	right.AddItem(d.adapterInfo, 0, 1, false)
	right.AddItem(help, 1, 0, false)

	split := tview.NewFlex()
	split.AddItem(d.adapterList, 0, 1, true)
	split.AddItem(right, 0, 2, false)

	d.refreshAdapterList()
	return split
}

// refreshAdapterList rescans for devices. Runs on the UI goroutine.
func (d *Displayer) refreshAdapterList() {
	current := d.adapterList.GetCurrentItem()
	d.adapters = d.provider.Adapters()
	d.adapterList.Clear()

	for _, a := range d.adapters {
		marker := "  "
		if a.Connected {
			marker = "[green]>[white] "
		}
		detail := a.Detail
		if detail == "" {
			detail = "no description"
		}

		port := a.Port
		d.adapterList.AddItem(marker+port, "   "+detail, 0, func() {
			d.connectTo(port)
		})
	}

	if len(d.adapters) == 0 {
		d.adapterList.AddItem("No adapters found", "   press s to scan again", 0, nil)
	}
	if current < d.adapterList.GetItemCount() {
		d.adapterList.SetCurrentItem(current)
	}
	d.paintAdapterInfo()
}

func (d *Displayer) paintAdapterInfo() {
	auto := "[gray]off[white]"
	if d.provider.Autoconnect() {
		auto = "[green]on[white]"
	}

	status := "[red]not connected[white]"
	if d.provider.IsConnected() {
		status = "[green]connected[white]"
	}

	out := "\n"
	out += fmt.Sprintf("  Status        %s\n", status)
	out += fmt.Sprintf("  Adapter       %s\n", d.provider.Description())
	out += fmt.Sprintf("  Autoconnect   %s\n", auto)
	out += fmt.Sprintf("  Found         %d device(s)\n", len(d.adapters))

	if !d.provider.IsConnected() {
		out += "\n  [gray]Select a device and press enter.[white]\n"
	}
	d.adapterInfo.SetText(out)
}

// connectTo attaches to one device, off the UI goroutine so a slow probe does
// not freeze the screen.
func (d *Displayer) connectTo(port string) {
	d.flash("Connecting to " + port + "...")

	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, 30*time.Second)
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

func (d *Displayer) toggleAutoconnect() {
	on := !d.provider.Autoconnect()
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
		d.refreshAdapterList()
		d.flash(fmt.Sprintf("Scanned: %d device(s)", len(d.adapters)))
		return nil
	case 't', 'T':
		d.toggleAutoconnect()
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
