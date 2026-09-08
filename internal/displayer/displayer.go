// Package displayer renders the terminal UI.
package displayer

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/vehicle"
	"log/slog"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// pollInterval is how often live values are re-read. Each refresh is several
// serial round trips, so this is a floor set by the hardware, not taste.
const pollInterval = 2 * time.Second

// dtcInterval is deliberately slower: a full module scan is expensive and
// trouble codes do not change second to second.
const dtcInterval = 30 * time.Second

// snapshot is the last successfully read state.
//
// The UI renders only from here and never touches the provider on the draw
// path. A serial round trip in a draw callback stalls the whole terminal, and
// a slow adapter would make the app feel broken.
type snapshot struct {
	rpm        int
	volts      float64
	coolant    float64
	oil        float64
	sinceClear int
	codes      []dtc.DTC
	connected  bool
	// errs records why a field is stale, so a dash on screen can be
	// explained rather than just being blank.
	errs map[string]error
}

// Displayer owns the TUI.
type Displayer struct {
	app      *tview.Application
	pages    *tview.Pages
	provider obd.OBDProvider
	// baseResolver has no make applied; resolver is it narrowed to the
	// active vehicle. Both are only touched on the UI goroutine.
	baseResolver *dtc.Resolver
	resolver     *dtc.Resolver
	garage       *vehicle.Garage
	makes        []string
	theme        Theme

	ctx    context.Context
	cancel context.CancelFunc

	// state is written by the poll goroutines and read on the draw path.
	state   snapshot
	stateMu chan struct{} // 1-buffered, used as a mutex

	// scanning is a 1-buffered channel held for the length of a module
	// scan. Two scans on one serial line retarget the adapter under each
	// other, so a second request is refused rather than queued.
	scanning chan struct{}

	dashPages   *tview.Pages
	rpmText     *tview.TextView
	coolantText *tview.TextView
	odoText     *tview.TextView
	oilText     *tview.TextView
	voltsText   *tview.TextView
	statusText  *tview.TextView
	helpText    *tview.TextView
	dtcTable    *tview.Table
	dtcProgress *tview.TextView
	detailText  *tview.TextView
	// shown is the sorted order the table was last drawn in, so a selected
	// row can be mapped back to the code it displays.
	shown         []dtc.DTC
	adapterForm   *tview.Form
	adapterDrop   *tview.DropDown
	autoCheck     *tview.Checkbox
	adapterInfo   *tview.TextView
	adapters      []obd.Adapter
	connectedPort string
	brand         *tview.TextView
	vehicleList   *tview.List
	vehicleInfo   *tview.TextView
	flashText     *tview.TextView
}

func New(provider obd.OBDProvider, resolver *dtc.Resolver, garage *vehicle.Garage, makes []string) *Displayer {
	ctx, cancel := context.WithCancel(context.Background())

	if garage == nil {
		garage = &vehicle.Garage{}
	}

	d := &Displayer{
		app:          tview.NewApplication(),
		pages:        tview.NewPages(),
		provider:     provider,
		baseResolver: resolver,
		resolver:     resolver,
		garage:       garage,
		makes:        makes,
		theme:        ThemeTerminal,
		ctx:          ctx,
		cancel:       cancel,
		stateMu:      make(chan struct{}, 1),
		scanning:     make(chan struct{}, 1),
	}
	d.state.errs = map[string]error{}
	d.stateMu <- struct{}{}

	// A vehicle chosen in a previous session applies from the first frame.
	if active, ok := garage.Active(); ok && active.Make != "" {
		d.resolver = resolver.WithMake(active.Make)
	}
	return d
}

// connected reports the last observed link state.
func (d *Displayer) connected() bool {
	d.lock()
	defer d.unlock()
	return d.state.connected
}

func (d *Displayer) lock()   { <-d.stateMu }
func (d *Displayer) unlock() { d.stateMu <- struct{}{} }

func (d *Displayer) Run() error {
	theme = d.theme
	applyTheme()

	d.brand = tview.NewTextView().SetDynamicColors(true).SetText(wordmark)

	d.statusText = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)

	d.helpText = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)

	// Status on the first two rows, keys on the third, all beside the
	// wordmark rather than below it: a separate help row cost a line of
	// the table for one line of text.
	side := tview.NewFlex().SetDirection(tview.FlexRow)
	side.AddItem(d.statusText, 2, 0, false)
	side.AddItem(d.helpText, 1, 0, false)

	banner := tview.NewFlex()
	banner.AddItem(d.brand, len([]rune(wordmarkLine))+2, 0, false)
	banner.AddItem(side, 0, 1, false)

	d.flashText = tview.NewTextView().SetDynamicColors(true)

	d.pages.AddPage("dashboard", d.buildDashboard(), true, true)
	d.pages.AddPage("dtc", d.buildDTC(), true, false)
	d.pages.AddPage("vehicle", d.buildVehicle(), true, false)
	d.pages.AddPage("adapter", d.buildAdapter(), true, false)
	d.pages.AddPage(detailPage, d.buildDetail(), true, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(banner, 3, 0, false)
	root.AddItem(d.pages, 0, 1, true)
	root.AddItem(d.flashText, 1, 0, false)

	d.app.SetRoot(root, true)
	d.app.SetInputCapture(d.onKey)

	// Paint the whole screen before each frame. Layout rounding and any
	// region a primitive does not cover would otherwise keep the
	// terminal's own background, which is what showed at the edges.
	d.app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		screen.Fill(' ', tcell.StyleDefault.Background(theme.background))
		return false
	})

	// Connecting is the first thing to do, so start where it happens,
	// unless autoconnect already attached.
	landing := "adapter"
	if d.provider.IsConnected() {
		landing = "dashboard"
	}
	d.showPage(landing)

	go d.pollLive()
	go d.pollDTCs()
	go d.watchConnection()

	return d.app.Run()
}

func (d *Displayer) onKey(event *tcell.EventKey) *tcell.EventKey {
	// While the make picker is up it owns the keyboard, or typing "m" to
	// filter would be read as a command.
	if name, _ := d.pages.GetFrontPage(); name == makePickerPage {
		return event
	}

	// An open dropdown owns the keyboard too. Its list takes letters to jump
	// between options, and the page shortcuts below would steal them: typing
	// "d" to reach /dev/ttyUSB0 would leave for the dashboard instead.
	if d.adapterDrop != nil && d.adapterDrop.IsOpen() {
		return event
	}

	switch event.Rune() {
	case 'q', 'Q':
		d.Shutdown()
		return nil
	case 'd', 'D':
		d.showPage("dashboard")
		return nil
	case 'c', 'C':
		d.showPage("dtc")
		return nil
	case 'v', 'V':
		d.showPage("vehicle")
		return nil
	case 'a', 'A':
		d.showPage("adapter")
		return nil
	}

	// Page-local keys are chosen not to collide with the navigation above,
	// which is checked first.
	switch name, _ := d.pages.GetFrontPage(); name {
	case "vehicle":
		return d.onVehicleKey(event)
	case "adapter":
		return d.onAdapterKey(event)
	case "dtc":
		if event.Rune() == 'r' || event.Rune() == 'R' {
			d.rescanDTCs()
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			d.showDetail()
			return nil
		}
	case detailPage:
		if event.Key() == tcell.KeyEscape {
			d.showPage("dtc")
			return nil
		}
	}
	return event
}

// showPage switches page and updates the key hints, which differ per page.
func (d *Displayer) showPage(name string) {
	d.pages.SwitchToPage(name)
	d.paintHelp(name)
}

// nav is the page switcher. Letters rather than digits: "2 Codes" reads as a
// count of codes, which is exactly the wrong thing on this screen.
const nav = "[::b]d[::-] Dashboard  [::b]c[::-] Codes  [::b]v[::-] Vehicle  " +
	"[::b]a[::-] Adapter  [::b]q[::-] Quit"

func (d *Displayer) paintHelp(page string) {
	help := nav
	if page == "dtc" {
		help = "[::b]enter[::-] Detail  [::b]r[::-] Rescan  " + nav
	}
	d.helpText.SetText(help + " ")
}

// flash shows a transient message under the page.
func (d *Displayer) flash(message string) {
	if d.flashText == nil {
		return
	}
	d.flashText.SetText(" " + message + "[white]")
}

func (d *Displayer) Shutdown() {
	d.cancel()
	d.provider.Stop()
	d.app.Stop()
}

func (d *Displayer) buildDashboard() tview.Primitive {
	d.rpmText = tview.NewTextView().SetDynamicColors(true)
	d.coolantText = tview.NewTextView().SetDynamicColors(true)
	d.odoText = tview.NewTextView().SetDynamicColors(true)
	d.oilText = tview.NewTextView().SetDynamicColors(true)
	d.voltsText = tview.NewTextView().SetDynamicColors(true)

	live := tview.NewFlex().SetDirection(tview.FlexRow)
	live.SetBorder(true).SetTitle(" Live data ")
	for _, tv := range []*tview.TextView{d.rpmText, d.coolantText, d.oilText, d.voltsText, d.odoText} {
		live.AddItem(tv, 1, 0, false)
	}

	// While disconnected the card would show a column of dashes, which on a
	// dashboard reads as measured values. Show why there is nothing instead.
	waiting := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("\n[yellow]No vehicle connected[white]\n\n" +
			"Press [::b]a[::-] to choose an adapter and connect.")
	waiting.SetBorder(true).SetTitle(" Live data ")

	d.dashPages = tview.NewPages()
	d.dashPages.AddPage("waiting", waiting, true, true)
	d.dashPages.AddPage("live", live, true, false)
	return d.dashPages
}

// dtcColumns are laid out so the two facts that were previously thrown away,
// which ECU reported the code and how confirmed it is, lead the row.
var dtcColumns = []string{"Module", "Code", "Status", "Description", "Source"}

func (d *Displayer) buildDTC() tview.Primitive {
	d.dtcTable = tview.NewTable().SetFixed(1, 0).SetSelectable(true, false)
	d.dtcTable.SetBorder(true).SetTitle(" Trouble codes ")

	// This row used to be a fixed reminder of the r key, which the help
	// line above already carries. A scan is tens of seconds of serial
	// traffic with nothing else to show for it, so the row reports that
	// instead.
	d.dtcProgress = tview.NewTextView().SetDynamicColors(true)

	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(d.dtcTable, 0, 1, true)
	flex.AddItem(d.dtcProgress, 1, 0, false)

	d.paintScanIdle()
	d.renderDTCTable(nil)
	return flex
}

func statusColour(s dtc.Status) string {
	switch s {
	case dtc.Stored:
		return "red"
	case dtc.Pending:
		return "yellow"
	case dtc.Permanent:
		return "fuchsia"
	}
	return "white"
}

// renderDTCTable rebuilds the table. It must run on the UI goroutine.
func (d *Displayer) renderDTCTable(codes []dtc.DTC) {
	d.dtcTable.Clear()

	for c, name := range dtcColumns {
		d.dtcTable.SetCell(0, c, tview.NewTableCell(name).
			SetSelectable(false).
			SetAttributes(tcell.AttrBold).
			SetTextColor(tcell.ColorYellow))
	}

	d.shown = nil

	if len(codes) == 0 {
		// "No faults" and "we cannot see the car" look identical in an
		// empty table, and only one of them is good news.
		message := "No trouble codes reported"
		if !d.connected() {
			message = "Not connected. Press a to choose an adapter"
		}
		d.dtcTable.SetCell(1, 0, tview.NewTableCell("-"))
		d.dtcTable.SetCell(1, 3, tview.NewTableCell(message))
		d.dtcTable.SetTitle(" Trouble codes ")
		return
	}

	// Group by module, then by severity, so one ECU's faults read together
	// and confirmed problems sit above unconfirmed ones.
	sorted := make([]dtc.DTC, len(codes))
	copy(sorted, codes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Module != sorted[j].Module {
			return sorted[i].Module < sorted[j].Module
		}
		if sorted[i].Status != sorted[j].Status {
			return sorted[i].Status < sorted[j].Status
		}
		return sorted[i].Code < sorted[j].Code
	})

	d.shown = sorted

	var stored, pending, permanent int
	lastModule := ""

	for i, code := range sorted {
		def := d.resolver.Describe(code)

		switch code.Status {
		case dtc.Stored:
			stored++
		case dtc.Pending:
			pending++
		case dtc.Permanent:
			permanent++
		}

		// Print the module once per group; repeating it on every row is
		// noise that makes the grouping harder to see.
		module := code.Module
		if module == "" {
			module = "unattributed"
		}
		shown := module
		if module == lastModule {
			shown = ""
		}
		lastModule = module

		row := i + 1
		d.dtcTable.SetCell(row, 0, tview.NewTableCell(shown).SetTextColor(tcell.ColorAqua))
		d.dtcTable.SetCell(row, 1, tview.NewTableCell(code.FullCode()).SetAttributes(tcell.AttrBold))
		status := code.Status.String()
		if code.WarningActive() {
			// The ECU is asking for a warning lamp, which is as close as
			// UDS comes to saying this one matters now.
			status += " !"
		}
		d.dtcTable.SetCell(row, 2, tview.NewTableCell(
			fmt.Sprintf("[%s]%s[white]", statusColour(code.Status), status)).
			SetExpansion(0))
		d.dtcTable.SetCell(row, 3, tview.NewTableCell(describe(code, def)).
			SetExpansion(1).
			SetMaxWidth(64))
		d.dtcTable.SetCell(row, 4, tview.NewTableCell(shortSource(def.Source)).
			SetTextColor(tcell.ColorGray))
	}

	d.dtcTable.SetTitle(fmt.Sprintf(
		" Trouble codes: %d stored, %d pending, %d permanent across %d module(s) ",
		stored, pending, permanent, countModules(sorted)))
}

// describe combines the catalogued meaning of a code with what its failure
// type adds. UDS codes name the component and the manner of its failure
// separately, and both are needed before the fault is actionable.
func describe(code dtc.DTC, def dtc.Definition) string {
	failure := code.FailureTypeName()
	if failure == "" {
		return def.Description
	}
	return fmt.Sprintf("%s (%s)", def.Description, failure)
}

// shortSource abbreviates provenance to fit a narrow column while still
// distinguishing a standards-backed definition from a guess.
func shortSource(source string) string {
	if source == "derived from encoding" {
		return "derived"
	}
	return source
}

func countModules(codes []dtc.DTC) int {
	seen := map[string]struct{}{}
	for _, c := range codes {
		seen[c.Module] = struct{}{}
	}
	return len(seen)
}

// pollLive refreshes the dashboard values.
func (d *Displayer) pollLive() {
	d.refreshLive()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.refreshLive()
		}
	}
}

func (d *Displayer) refreshLive() {
	ctx, cancel := context.WithTimeout(d.ctx, pollInterval)
	defer cancel()

	rpm, rpmErr := d.provider.GetRPM(ctx)
	volts, voltsErr := d.provider.GetVoltage(ctx)
	coolant, coolantErr := d.provider.GetCoolantTemp(ctx)
	oil, oilErr := d.provider.GetOilTemp(ctx)
	km, kmErr := d.provider.GetDistanceSinceClear(ctx)
	connected := d.provider.IsConnected()

	d.lock()
	// A failed read leaves the previous value in place rather than
	// flashing a zero, which on a dashboard reads as a real measurement.
	if rpmErr == nil {
		d.state.rpm = rpm
	}
	if voltsErr == nil {
		d.state.volts = volts
	}
	if coolantErr == nil {
		d.state.coolant = coolant
	}
	if oilErr == nil {
		d.state.oil = oil
	}
	if kmErr == nil {
		d.state.sinceClear = km
	}
	linkChanged := d.state.connected != connected
	d.state.connected = connected
	d.state.errs["rpm"] = rpmErr
	d.state.errs["volts"] = voltsErr
	d.state.errs["coolant"] = coolantErr
	d.state.errs["oil"] = oilErr
	d.state.errs["odometer"] = kmErr
	state := d.state
	codes := d.state.codes
	d.unlock()

	d.app.QueueUpdateDraw(func() {
		d.paintDashboard(state)
		if linkChanged {
			d.renderDTCTable(codes)
		}
	})

	// A link that has just come up has codes worth reading immediately
	// rather than at the next slow tick.
	if linkChanged && connected {
		go d.refreshDTCs()
	}
}

func value(err error, format string, args ...any) string {
	if err != nil {
		return "-"
	}
	return fmt.Sprintf(format, args...)
}

// voltage colours the reading against what a healthy system does: about 12.6V
// at rest, 13.5-14.8V with the engine running. Outside that band on either
// side is worth seeing without having to know the numbers.
func voltage(s snapshot) string {
	if s.errs["volts"] != nil {
		return "-"
	}
	switch {
	case s.volts < 11.8:
		return fmt.Sprintf("[red]%.1f[white]  flat or discharging", s.volts)
	case s.volts > 14.9:
		return fmt.Sprintf("[red]%.1f[white]  overcharging", s.volts)
	case s.volts > 13.2:
		return fmt.Sprintf("[green]%.1f[white]  charging", s.volts)
	}
	return fmt.Sprintf("%.1f", s.volts)
}

func (d *Displayer) paintDashboard(s snapshot) {
	if s.connected {
		d.dashPages.SwitchToPage("live")
	} else {
		d.dashPages.SwitchToPage("waiting")
	}

	d.rpmText.SetText("  RPM              " + value(s.errs["rpm"], "%d", s.rpm))
	d.coolantText.SetText("  Coolant (°C)     " + value(s.errs["coolant"], "%.1f", s.coolant))
	d.oilText.SetText("  Oil temp (°C)    " + value(s.errs["oil"], "%.1f", s.oil))
	d.voltsText.SetText("  Battery (V)      " + voltage(s))
	d.odoText.SetText("  Since clear (km) " + value(s.errs["odometer"], "%d", s.sinceClear))

	dot, state := "[red]\u25cf[white]", "not connected"
	if s.connected {
		dot, state = "[green]\u25cf[white]", "connected"
	}

	detail := d.provider.Description()
	if make := d.codeMake(); make != "" {
		detail += "  [aqua]" + make + "[white]"
	}
	status := fmt.Sprintf("%s %s \n[gray]%s[white] ", dot, state, strings.TrimSpace(detail))
	d.statusText.SetText(status)
}

// pollDTCs rescans the modules on a slow cadence.
func (d *Displayer) pollDTCs() {
	d.refreshDTCs()

	ticker := time.NewTicker(dtcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.refreshDTCs()
		}
	}
}

// rescanDTCs is the r key: widen the module list back out and scan, on its own
// goroutine so the keypress returns immediately.
//
// The automatic poll narrows the list to the modules that answered, which is
// right for a refresh every thirty seconds and wrong here. Someone pressing r
// is asking about the whole car, and a module that was asleep the first time
// would otherwise never be looked at again this session.
func (d *Displayer) rescanDTCs() {
	go func() {
		d.provider.RescanModules()
		d.scanDTCs()
	}()
}

// refreshDTCs is the automatic scan, over whatever module list the provider
// has settled on.
func (d *Displayer) refreshDTCs() { d.scanDTCs() }

// scanTimeout bounds one walk. It is longer than the interval between walks
// because a cold scan pays a timeout for every address that turns out not to
// be fitted, and a scan cut off half way is worse than a slow one.
const scanTimeout = 90 * time.Second

func (d *Displayer) scanDTCs() {
	// One scan at a time. Two walks share one serial line and retarget the
	// adapter under each other, so the second would return nothing useful
	// and corrupt the first.
	select {
	case d.scanning <- struct{}{}:
	default:
		d.app.QueueUpdateDraw(func() {
			d.paintScan("[yellow]A scan is already running")
		})
		return
	}
	defer func() { <-d.scanning }()

	if !d.provider.IsConnected() {
		d.app.QueueUpdateDraw(func() {
			d.paintScan("[yellow]Not connected: press [::b]a[::-] to choose an adapter")
		})
		return
	}

	ctx, cancel := context.WithTimeout(d.ctx, scanTimeout)
	defer cancel()

	started := time.Now()
	var probed, answered int

	codes, err := d.provider.GetDTCs(ctx, func(p obd.ScanProgress) {
		probed, answered = p.Index, p.Answered
		if p.Done {
			return
		}
		d.app.QueueUpdateDraw(func() {
			d.paintScan(fmt.Sprintf("[yellow]Scanning %s ... [gray]module %d of %d",
				p.Module, p.Index, p.Total))
		})
	})

	// A scan that ran out of time still found whatever it found before the
	// deadline, and those codes are real. Keep them and say the walk was
	// cut short rather than throwing the lot away.
	if err != nil && len(codes) == 0 {
		slog.Debug("Trouble-code scan failed", "error", err)
		d.app.QueueUpdateDraw(func() {
			d.paintScan("[red]Scan failed: " + err.Error())
		})
		return
	}

	d.lock()
	d.state.codes = codes
	d.unlock()

	elapsed := time.Since(started).Round(time.Second)

	finished := time.Now()

	d.app.QueueUpdateDraw(func() {
		d.renderDTCTable(codes)

		// Say what was looked at, not just what was found. "No faults"
		// after a scan that never reached the bus reads the same as
		// "no faults" after a clean one, and only one is good news.
		// Report what answered, not just what was asked. An empty table
		// after four of nine modules replied means five ECUs were never
		// heard from, which is not the same as a car with no faults.
		result := fmt.Sprintf("[green]%d of %d module(s) answered in %s: found %d code(s)",
			answered, probed, elapsed, len(codes))
		if answered < probed {
			result = fmt.Sprintf("[yellow]%d of %d module(s) answered in %s: found %d code(s). "+
				"[gray]The rest did not reply; see the log",
				answered, probed, elapsed, len(codes))
		}
		if err != nil {
			result = fmt.Sprintf("[red]Scan cut short after %d module(s) in %s (%v): found %d code(s)",
				probed, elapsed, err, len(codes))
		}
		d.paintScan(result + "   [gray]at " + finished.Format("15:04:05"))
	})
}

// paintScan writes the row under the code table. It must run on the UI
// goroutine.
func (d *Displayer) paintScan(message string) {
	if d.dtcProgress == nil {
		return
	}
	d.dtcProgress.SetText(" " + message + "[white]")
}

// paintScanIdle is the row before anything has been scanned.
func (d *Displayer) paintScanIdle() {
	d.paintScan("[gray]Not scanned yet")
}

// wordmark is the name in box-drawing capitals, three rows so it sits level
// with the connection state beside it.
const wordmark = "[#f7ac16]┌─┐┌─┐┬─┐┌─┐┌─┐\n│  ├─┤├┬┘│ ┬│ │\n└─┘┴ ┴┴└─└─┘└─┘[white]"

// wordmarkLine is one row of it, for measuring the column width.
const wordmarkLine = "┌─┐┌─┐┬─┐┌─┐┌─┐"

// SetTheme chooses the palette. It must be called before Run.
func (d *Displayer) SetTheme(t Theme) { d.theme = t }
