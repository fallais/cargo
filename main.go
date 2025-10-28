package main

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"cargo/internal/obd"

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
	rpmText     *tview.TextView
	coolantText *tview.TextView
	statusText  *tview.TextView
	helpText    *tview.TextView
	dtcTable    *tview.Table
}

func NewDisplayer(provider obd.OBDProvider) *Displayer {
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
	title := tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText("car - k9n style CLI")
	d.statusText = tview.NewTextView().SetTextAlign(tview.AlignCenter)
	d.helpText = tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText("Keys: 1 Dashboard  2 DTC  q Quit")

	headerFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	headerFlex.AddItem(title, 1, 0, false)
	headerFlex.AddItem(d.statusText, 1, 0, false)
	headerFlex.AddItem(d.helpText, 1, 0, false)

	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(headerFlex, 3, 0, false)
	flex.AddItem(dashboard, 0, 1, true)

	d.dtcTable = dtc
	d.tabs.AddPage("dashboard", flex, true, true)
	d.tabs.AddPage("dtc", dtc, true, false)

	d.app.SetRoot(d.tabs, true)
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

	// central BeforeDraw to update UI elements
	d.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		// update values
		if d.rpmText != nil && d.coolantText != nil {
			rpm, _ := d.provider.GetRPM()
			cool, _ := d.provider.GetCoolantTemp()
			d.rpmText.SetText(fmt.Sprintf("[yellow]RPM:[white] %d", rpm))
			d.coolantText.SetText(fmt.Sprintf("[yellow]Coolant (C):[white] %.1f", cool))
		}
		if d.statusText != nil {
			status := "disconnected"
			if d.provider.Connected() {
				status = "connected"
			}
			d.statusText.SetText(fmt.Sprintf("Status: %s", status))
		}
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

	infoFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	infoFlex.AddItem(rpmText, 3, 0, false)
	infoFlex.AddItem(coolantText, 3, 0, false)

	// cache pointers for centralized updates
	d.rpmText = rpmText
	d.coolantText = coolantText

	return infoFlex
}

func (d *Displayer) buildDTC() *tview.Table {
	tbl := tview.NewTable().SetBorders(true)
	tbl.SetCell(0, 0, tview.NewTableCell("Code").SetSelectable(false).SetAlign(tview.AlignCenter))
	tbl.SetCell(0, 1, tview.NewTableCell("Description").SetSelectable(false).SetAlign(tview.AlignCenter))

	// update in refresh loop; provide a method to redraw
	return tbl
}

func (d *Displayer) refreshLoop() {
	ticker := time.NewTicker(1 * time.Second)
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

func detectPlatformSerialDev() string {
	if runtime.GOOS == "windows" {
		// stub: could enumerate COM ports like COM1..COM256
		return "COM3"
	}
	// linux/unix stub
	return "/dev/ttyUSB0"
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// Try to build a real serial provider first; fall back to mock if no
	// device is available quickly.
	dev := detectPlatformSerialDev()
	var provider obd.OBDProvider = obd.NewSerialOBD(dev, 38400)
	_ = provider.Start(context.Background())
	// wait briefly to see if it connects
	time.Sleep(1500 * time.Millisecond)
	if !provider.Connected() {
		// fallback
		provider.Stop()
		provider = obd.NewMockOBD()
	}
	d := NewDisplayer(provider)

	// small README-like startup
	fmt.Printf("Starting displayer (platform default dev: %s)\n", detectPlatformSerialDev())

	if err := d.Run(); err != nil {
		fmt.Printf("error: %v\n", err)
	}
}
