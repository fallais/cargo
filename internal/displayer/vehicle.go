package displayer

import (
	"context"
	"fmt"
	"time"

	"cargo/internal/vehicle"
	"cargo/pkg/log"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"go.uber.org/zap"
)

// The vehicle page is where the user says which car this is.
//
// It matters more than it looks: around 700 manufacturer trouble codes mean
// different things on different marques, so until the make is known the
// catalog has to refuse to answer for them. Reading the VIN turns that from a
// question into something the car tells us, which is what the established
// scan tools do.

func (d *Displayer) buildVehicle() tview.Primitive {
	d.vehicleList = tview.NewList().ShowSecondaryText(true)
	d.vehicleList.SetBorder(true).SetTitle(" Garage ")
	d.vehicleList.SetChangedFunc(func(int, string, string, rune) { d.paintVehicleInfo() })

	d.vehicleInfo = tview.NewTextView().SetDynamicColors(true)
	d.vehicleInfo.SetBorder(true).SetTitle(" Selected ")

	help := tview.NewTextView().
		SetDynamicColors(true).
		SetText("[::b]enter[::-] use   [::b]v[::-] read VIN from car   " +
			"[::b]m[::-] set make   [::b]n[::-] add   [::b]x[::-] remove")

	right := tview.NewFlex().SetDirection(tview.FlexRow)
	right.AddItem(d.vehicleInfo, 0, 1, false)
	right.AddItem(help, 1, 0, false)

	split := tview.NewFlex()
	split.AddItem(d.vehicleList, 0, 1, true)
	split.AddItem(right, 0, 2, false)

	d.refreshVehicleList()
	return split
}

// refreshVehicleList rebuilds the garage list. Runs on the UI goroutine.
func (d *Displayer) refreshVehicleList() {
	current := d.vehicleList.GetCurrentItem()
	d.vehicleList.Clear()

	active, hasActive := d.garage.Active()
	for i, v := range d.garage.Vehicles {
		marker := "  "
		if hasActive && v.VIN == active.VIN && v.Name == active.Name {
			marker = "[green]>[white] "
		}

		secondary := v.VIN
		if secondary == "" {
			secondary = "no VIN"
		}
		if v.Make == "" {
			secondary += "  [yellow](no make set)[white]"
		}

		index := i
		d.vehicleList.AddItem(marker+v.Label(), "   "+secondary, 0, func() {
			d.useVehicle(index)
		})
	}

	if len(d.garage.Vehicles) == 0 {
		d.vehicleList.AddItem("No vehicles yet", "   press v to read the VIN, or n to add one", 0, nil)
	}

	if current < d.vehicleList.GetItemCount() {
		d.vehicleList.SetCurrentItem(current)
	}
	d.paintVehicleInfo()
}

// paintVehicleInfo describes the highlighted vehicle.
func (d *Displayer) paintVehicleInfo() {
	if len(d.garage.Vehicles) == 0 {
		d.vehicleInfo.SetText("\n  No vehicle configured.\n\n" +
			"  [yellow]Manufacturer-specific codes cannot be resolved\n" +
			"  without knowing the make.[white]\n\n" +
			"  Press [::b]v[::-] to read the VIN from the car, or [::b]m[::-]\n" +
			"  to choose the make yourself.")
		return
	}

	index := d.vehicleList.GetCurrentItem()
	if index >= len(d.garage.Vehicles) {
		return
	}
	v := d.garage.Vehicles[index]

	var b fmt.Stringer = &vehicleSummary{profile: v, active: d.isActive(v)}
	d.vehicleInfo.SetText(b.String())
}

func (d *Displayer) isActive(v vehicle.Profile) bool {
	active, ok := d.garage.Active()
	return ok && active.VIN == v.VIN && active.Name == v.Name
}

// vehicleSummary renders one profile.
type vehicleSummary struct {
	profile vehicle.Profile
	active  bool
}

func (s *vehicleSummary) String() string {
	v := s.profile

	out := "\n"
	out += fmt.Sprintf("  Name    %s\n", orDash(v.Label()))
	out += fmt.Sprintf("  Make    %s\n", orDash(v.Make))
	out += fmt.Sprintf("  Model   %s\n", orDash(v.Model))
	out += fmt.Sprintf("  Year    %s\n", orDash(yearText(v.Year)))
	out += fmt.Sprintf("  VIN     %s\n", orDash(v.VIN))

	out += "\n"
	if s.active {
		out += "  [green]In use for code lookups.[white]\n"
	} else {
		out += "  [gray]Press enter to use this vehicle.[white]\n"
	}

	if v.Make == "" {
		out += "\n  [yellow]No make set, so manufacturer codes will be\n" +
			"  reported as ambiguous rather than guessed.[white]\n"
	}
	if v.Detected {
		out += "\n  [gray]Identified from the vehicle's own VIN.[white]\n"
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "[gray]not set[white]"
	}
	return s
}

func yearText(year int) string {
	if year <= 0 {
		return ""
	}
	return fmt.Sprint(year)
}

// useVehicle makes a profile active and re-resolves the codes on screen.
func (d *Displayer) useVehicle(index int) {
	if index < 0 || index >= len(d.garage.Vehicles) {
		return
	}

	d.garage.SetActive(index)
	d.applyVehicle()
	d.saveGarage()
	d.refreshVehicleList()
}

// applyVehicle points the resolver at the active vehicle's make.
//
// The already-scanned codes are re-rendered rather than re-read: the codes did
// not change, only what we can say about them.
func (d *Displayer) applyVehicle() {
	make := ""
	if active, ok := d.garage.Active(); ok {
		make = active.Make
	}

	d.lock()
	d.resolver = d.baseResolver.WithMake(make)
	codes := d.state.codes
	d.unlock()

	d.renderDTCTable(codes)
}

func (d *Displayer) saveGarage() {
	if err := d.garage.Save(); err != nil {
		log.Warn("Could not save the garage", zap.Error(err))
		d.flash("[red]Could not save: " + err.Error())
	}
}

// detectVehicle reads the VIN from the car.
func (d *Displayer) detectVehicle() {
	d.flash("Reading VIN from the vehicle...")

	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, 10*time.Second)
		defer cancel()

		raw, err := d.provider.GetVIN(ctx)
		if err != nil {
			d.app.QueueUpdateDraw(func() {
				// Mode 09 is only mandatory from the 2005 model year, so
				// an older car legitimately cannot answer this.
				d.flash("[yellow]No VIN from this vehicle: " + err.Error())
			})
			return
		}

		decoded, err := vehicle.ParseVIN(raw)
		if err != nil {
			d.app.QueueUpdateDraw(func() {
				d.flash("[red]Unreadable VIN: " + err.Error())
			})
			return
		}

		d.app.QueueUpdateDraw(func() {
			index := d.garage.Upsert(vehicle.FromVIN(decoded))
			d.garage.SetActive(index)
			d.applyVehicle()
			d.saveGarage()
			d.refreshVehicleList()
			d.vehicleList.SetCurrentItem(index)

			switch {
			case decoded.Make == "":
				d.flash(fmt.Sprintf("[yellow]VIN %s read, but %s is not a known manufacturer prefix. Pick the make with m.",
					decoded.Raw, decoded.WMI))
			case !decoded.CheckDigitValid:
				d.flash(fmt.Sprintf("[yellow]VIN %s read as %s, but its check digit does not verify.",
					decoded.Raw, decoded.Make))
			default:
				d.flash(fmt.Sprintf("[green]Identified: %s", vehicle.FromVIN(decoded).Label()))
			}
		})
	}()
}

// makePickerPage is the overlay name for the make chooser.
const makePickerPage = "makepicker"

// chooseMake opens a picker over the current page.
func (d *Displayer) chooseMake() {
	if len(d.garage.Vehicles) == 0 {
		d.garage.Upsert(vehicle.Profile{Name: "My vehicle"})
		d.refreshVehicleList()
	}

	index := d.vehicleList.GetCurrentItem()
	if index >= len(d.garage.Vehicles) {
		index = len(d.garage.Vehicles) - 1
	}

	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitle(" Make (esc to cancel) ")

	// Only makes the catalog can actually distinguish are offered: a longer
	// list would imply knowledge the data does not have.
	for _, m := range d.makes {
		make := m
		list.AddItem(make, "", 0, func() {
			d.garage.Vehicles[index].Make = make
			d.garage.Vehicles[index].Detected = false
			d.garage.SetActive(index)
			d.applyVehicle()
			d.saveGarage()
			d.pages.RemovePage(makePickerPage)
			d.refreshVehicleList()
			d.flash("[green]Make set to " + make)
		})
	}

	list.SetDoneFunc(func() { d.pages.RemovePage(makePickerPage) })

	d.pages.AddPage(makePickerPage, centred(list, 34, 22), true, true)
}

// centred puts a primitive in the middle of the screen at a fixed size.
func centred(p tview.Primitive, width, height int) tview.Primitive {
	row := tview.NewFlex().SetDirection(tview.FlexRow)
	row.AddItem(nil, 0, 1, false)
	row.AddItem(p, height, 0, true)
	row.AddItem(nil, 0, 1, false)

	col := tview.NewFlex()
	col.AddItem(nil, 0, 1, false)
	col.AddItem(row, width, 0, true)
	col.AddItem(nil, 0, 1, false)
	return col
}

// addVehicle appends a blank profile for the user to fill in.
func (d *Displayer) addVehicle() {
	d.garage.Upsert(vehicle.Profile{Name: fmt.Sprintf("Vehicle %d", len(d.garage.Vehicles)+1)})
	d.refreshVehicleList()
	d.vehicleList.SetCurrentItem(len(d.garage.Vehicles) - 1)
	d.flash("Added a vehicle. Press m to set its make.")
}

// removeVehicle deletes the highlighted profile.
func (d *Displayer) removeVehicle() {
	index := d.vehicleList.GetCurrentItem()
	if index >= len(d.garage.Vehicles) {
		return
	}

	label := d.garage.Vehicles[index].Label()
	d.garage.Remove(index)
	d.applyVehicle()
	d.saveGarage()
	d.refreshVehicleList()
	d.flash("Removed " + label)
}

// onVehicleKey handles keys while the vehicle page is showing.
func (d *Displayer) onVehicleKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Rune() {
	case 'v', 'V':
		d.detectVehicle()
		return nil
	case 'm', 'M':
		d.chooseMake()
		return nil
	case 'n', 'N':
		d.addVehicle()
		return nil
	case 'x', 'X':
		d.removeVehicle()
		return nil
	}
	return event
}

// codeMake reports the make used for lookups, for the status line.
func (d *Displayer) codeMake() string {
	if active, ok := d.garage.Active(); ok && active.Make != "" {
		return active.Make
	}
	return ""
}
