// Package displayer renders the terminal UI.
package displayer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/fallais/cargo/internal/dtc"
	"github.com/fallais/cargo/internal/obd"
	"github.com/fallais/cargo/internal/vehicle"
	"github.com/fallais/cargo/pkg/log"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.uber.org/zap"
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
	coolant    float64
	oil        float64
	kilometres int
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

	ctx    context.Context
	cancel context.CancelFunc

	// state is written by the poll goroutines and read on the draw path.
	state   snapshot
	stateMu chan struct{} // 1-buffered, used as a mutex

	dashPages   *tview.Pages
	rpmText     *tview.TextView
	coolantText *tview.TextView
	odoText     *tview.TextView
	oilText     *tview.TextView
	statusText  *tview.TextView
	helpText    *tview.TextView
	dtcTable    *tview.Table
	dtcSummary  *tview.TextView
	vehicleList *tview.List
	vehicleInfo *tview.TextView
	flashText   *tview.TextView
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
		ctx:          ctx,
		cancel:       cancel,
		stateMu:      make(chan struct{}, 1),
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
	title := tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("cargo - command-line OBD-II tool")
	d.statusText = tview.NewTextView().SetTextAlign(tview.AlignCenter).SetDynamicColors(true)
	d.helpText = tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true)
	d.flashText = tview.NewTextView().SetDynamicColors(true)

	header := tview.NewFlex().SetDirection(tview.FlexRow)
	header.AddItem(title, 1, 0, false)
	header.AddItem(d.statusText, 1, 0, false)
	header.AddItem(d.helpText, 1, 0, false)

	d.pages.AddPage("dashboard", d.buildDashboard(), true, true)
	d.pages.AddPage("dtc", d.buildDTC(), true, false)
	d.pages.AddPage("vehicle", d.buildVehicle(), true, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(header, 3, 0, false)
	root.AddItem(d.pages, 0, 1, true)
	root.AddItem(d.flashText, 1, 0, false)

	d.paintHelp("dashboard")

	d.app.SetRoot(root, true)
	d.app.SetInputCapture(d.onKey)

	go d.pollLive()
	go d.pollDTCs()

	return d.app.Run()
}

func (d *Displayer) onKey(event *tcell.EventKey) *tcell.EventKey {
	// While the make picker is up it owns the keyboard, or typing "m" to
	// filter would be read as a command.
	if name, _ := d.pages.GetFrontPage(); name == makePickerPage {
		return event
	}

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
	case '3':
		d.showPage("vehicle")
		return nil
	case 'r', 'R':
		go d.refreshDTCs()
		return nil
	}

	if name, _ := d.pages.GetFrontPage(); name == "vehicle" {
		return d.onVehicleKey(event)
	}
	return event
}

// showPage switches page and updates the key hints, which differ per page.
func (d *Displayer) showPage(name string) {
	d.pages.SwitchToPage(name)
	d.paintHelp(name)
}

func (d *Displayer) paintHelp(page string) {
	common := "[::b]1[::-] Dashboard  [::b]2[::-] Codes  [::b]3[::-] Vehicle  [::b]q[::-] Quit"
	switch page {
	case "dtc":
		common = "[::b]r[::-] Rescan   " + common
	case "vehicle":
		common = "[::b]v[::-] Read VIN  [::b]m[::-] Make  [::b]n[::-] Add  [::b]x[::-] Remove   " + common
	}
	d.helpText.SetText(common)
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

	live := tview.NewFlex().SetDirection(tview.FlexRow)
	live.SetBorder(true).SetTitle(" Live data ")
	for _, tv := range []*tview.TextView{d.rpmText, d.coolantText, d.oilText, d.odoText} {
		live.AddItem(tv, 1, 0, false)
	}

	// While disconnected the card would show a column of dashes, which on a
	// dashboard reads as measured values. Show why there is nothing instead.
	waiting := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("\n[yellow]No vehicle connected[white]\n\n" +
			"Plug in an ELM327 adapter. Live data appears on its own.\n" +
			"[gray]Use --port to choose a device, or --mock to try the UI.[white]")
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

	d.dtcSummary = tview.NewTextView().SetDynamicColors(true)

	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	flex.AddItem(d.dtcSummary, 1, 0, false)
	flex.AddItem(d.dtcTable, 0, 1, true)

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

	if len(codes) == 0 {
		// "No faults" and "we cannot see the car" look identical in an
		// empty table, and only one of them is good news.
		message, summary := "No trouble codes reported", "[green]No faults[white]"
		if !d.connected() {
			message = "Not connected. Plug in an adapter and the scan will start on its own"
			summary = "[yellow]Waiting for a vehicle[white]"
		}
		d.dtcTable.SetCell(1, 0, tview.NewTableCell("-"))
		d.dtcTable.SetCell(1, 3, tview.NewTableCell(message))
		d.dtcSummary.SetText(summary)
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

	d.dtcSummary.SetText(fmt.Sprintf(
		"[red]%d stored[white]   [yellow]%d pending[white]   [fuchsia]%d permanent[white]   across %d module(s)",
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
	coolant, coolantErr := d.provider.GetCoolantTemp(ctx)
	oil, oilErr := d.provider.GetOilTemp(ctx)
	km, kmErr := d.provider.GetTotalKilometers(ctx)
	connected := d.provider.IsConnected()

	d.lock()
	// A failed read leaves the previous value in place rather than
	// flashing a zero, which on a dashboard reads as a real measurement.
	if rpmErr == nil {
		d.state.rpm = rpm
	}
	if coolantErr == nil {
		d.state.coolant = coolant
	}
	if oilErr == nil {
		d.state.oil = oil
	}
	if kmErr == nil {
		d.state.kilometres = km
	}
	linkChanged := d.state.connected != connected
	d.state.connected = connected
	d.state.errs["rpm"] = rpmErr
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

func (d *Displayer) paintDashboard(s snapshot) {
	if s.connected {
		d.dashPages.SwitchToPage("live")
	} else {
		d.dashPages.SwitchToPage("waiting")
	}

	d.rpmText.SetText("  RPM              " + value(s.errs["rpm"], "%d", s.rpm))
	d.coolantText.SetText("  Coolant (°C)     " + value(s.errs["coolant"], "%.1f", s.coolant))
	d.oilText.SetText("  Oil temp (°C)    " + value(s.errs["oil"], "%.1f", s.oil))
	d.odoText.SetText("  Distance (km)    " + value(s.errs["odometer"], "%d", s.kilometres))

	status := "[red]disconnected[white]"
	if s.connected {
		status = "[green]connected[white]"
	}
	line := fmt.Sprintf("%s - %s", status, d.provider.Description())
	if make := d.codeMake(); make != "" {
		line += fmt.Sprintf("  [aqua][%s][white]", make)
	}
	d.statusText.SetText(line)
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

func (d *Displayer) refreshDTCs() {
	ctx, cancel := context.WithTimeout(d.ctx, dtcInterval)
	defer cancel()

	codes, err := d.provider.GetDTCs(ctx)
	if err != nil {
		log.Debug("Trouble-code scan failed", zap.Error(err))
		return
	}

	d.lock()
	d.state.codes = codes
	d.unlock()

	d.app.QueueUpdateDraw(func() { d.renderDTCTable(codes) })
}
