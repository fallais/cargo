package displayer

import (
	"cargo/internal/obd"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Displayer handles the TUI and uses an internal OBDProvider.
// It exposes methods to run the UI and refresh periodic data.
type Displayer struct {
	app      *tview.Application
	tabs     *tview.Pages
	provider obd.OBDProvider
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex

	// UI elements cached for updates
	rpmText             *tview.TextView
	coolantText         *tview.TextView
	statusText          *tview.TextView
	totalKilometersText *tview.TextView
	oilTempText         *tview.TextView
	helpText            *tview.TextView
	dtcTable            *tview.Table
}

func New(provider obd.OBDProvider) *Displayer {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Displayer{
		app:      tview.NewApplication(),
		tabs:     tview.NewPages(),
		provider: provider,
		ctx:      ctx,
		cancel:   cancel,
	}
	return d
}

func (d *Displayer) Run() error {
	// start provider
	if err := d.provider.Start(d.ctx); err != nil {
		return err
	}
	// build UI
	dashboard := d.buildDashboard()
	dtc := d.buildDTC()

	// header area: title, status, help
	title := tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText("cargo - command-line OBD2 tool")
	d.statusText = tview.NewTextView().SetTextAlign(tview.AlignCenter).SetDynamicColors(true)
	d.helpText = tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText("Keys: 1 Dashboard  2 DTC  q Quit")

	headerFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	headerFlex.AddItem(title, 1, 0, false)
	headerFlex.AddItem(d.statusText, 1, 0, false)
	headerFlex.AddItem(d.helpText, 1, 0, false)

	// Create main layout with header always visible
	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	mainFlex.AddItem(headerFlex, 3, 0, false)

	// Add pages to tabs
	d.dtcTable = dtc
	d.tabs.AddPage("dashboard", dashboard, true, true)
	d.tabs.AddPage("dtc", dtc, true, false)

	// Add tabs to main layout
	mainFlex.AddItem(d.tabs, 0, 1, true)

	d.app.SetRoot(mainFlex, true)
	d.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'q', 'Q':
			d.Shutdown()
			return nil
		case '1':
			d.showPage("dashboard")
			return nil
		case '2':
			d.showPage("dtc")
			return nil
		}
		return event
	})

	// updateValues updates the displayed values.
	d.updateValues()

	// central BeforeDraw to update UI elements
	d.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		// update values
		d.updateValues()
		return false
	})

	// refresh loop
	go d.refreshLoop()

	if err := d.app.Run(); err != nil {
		return err
	}
	return nil
}

func (d *Displayer) Shutdown() {
	d.cancel()
	d.provider.Stop()
	d.app.Stop()
}

func (d *Displayer) showPage(name string) {
	d.tabs.SwitchToPage(name)
}

func (d *Displayer) buildDashboard() *tview.Flex {
	rpmText := tview.NewTextView().SetDynamicColors(true)
	coolantText := tview.NewTextView().SetDynamicColors(true)
	totalKilometersText := tview.NewTextView().SetDynamicColors(true)
	oilTempText := tview.NewTextView().SetDynamicColors(true)

	infoFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	infoFlex.AddItem(rpmText, 1, 0, false)
	infoFlex.AddItem(coolantText, 1, 0, false)
	infoFlex.AddItem(totalKilometersText, 1, 0, false)
	infoFlex.AddItem(oilTempText, 1, 0, false)

	// cache pointers for centralized updates
	d.rpmText = rpmText
	d.coolantText = coolantText
	d.totalKilometersText = totalKilometersText
	d.oilTempText = oilTempText

	return infoFlex
}

func (d *Displayer) buildDTC() *tview.Table {
	tbl := tview.NewTable().SetBorders(true)
	tbl.SetCell(0, 0, tview.NewTableCell("Code").SetSelectable(false).SetAlign(tview.AlignCenter))
	tbl.SetCell(0, 1, tview.NewTableCell("Description").SetSelectable(false).SetAlign(tview.AlignCenter))

	errs, _ := d.provider.GetErrors()
	for i, e := range errs {
		tbl.SetCell(i+1, 0, tview.NewTableCell(e.Code))
		tbl.SetCell(i+1, 1, tview.NewTableCell(e.Description))
	}

	return tbl
}

func (d *Displayer) updateValues() {
	rpm, _ := d.provider.GetRPM()
	cool, _ := d.provider.GetCoolantTemp()
	totalKilometers, _ := d.provider.GetTotalKilometers()
	oilTemp, _ := d.provider.GetOilTemp()

	d.rpmText.SetText(fmt.Sprintf("RPM: %d", rpm))
	d.coolantText.SetText(fmt.Sprintf("Coolant (C): %.1f", cool))
	d.totalKilometersText.SetText(fmt.Sprintf("Total Kilometers: %d", totalKilometers))
	d.oilTempText.SetText(fmt.Sprintf("Oil Temp (C): %.1f", oilTemp))

	d.helpText.SetText("[1 - Dashboard] [2 - DTC] [q - Quit]")

	if d.statusText != nil {
		status := "[red]disconnected[white]"
		if d.provider.IsConnected() {
			status = "[green]connected[white]"
		}
		d.statusText.SetText(fmt.Sprintf("Status: %s", status))
	}
}

func (d *Displayer) refreshLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// force redraw (BeforeDraw handles dashboard)
			d.app.QueueUpdateDraw(func() {})
			// update DTC table
			if page := d.tabs.GetPage("dtc"); page != nil {
				if tbl, ok := page.(*tview.Table); ok {
					errs, _ := d.provider.GetErrors()
					// clear rows except header
					for r := tbl.GetRowCount() - 1; r >= 1; r-- {
						tbl.RemoveRow(r)
					}
					for i, e := range errs {
						tbl.SetCell(i+1, 0, tview.NewTableCell(e.Code))
						tbl.SetCell(i+1, 1, tview.NewTableCell(e.Description))
					}
				}
			}
		}
	}
}
