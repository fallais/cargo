package displayer

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Theme names the two coherent choices.
//
// The original fault was not using colour but mixing: tview paints
// tcell.ColorBlack, which is the terminal's palette colour 0 rather than its
// actual background. On Ubuntu those differ, so painted cells came out
// near-black against a purple surround wherever nothing painted at all.
// Either answer works as long as it is applied everywhere.
type Theme struct {
	background tcell.Color
	text       tcell.Color
	dim        tcell.Color
	border     tcell.Color
	title      tcell.Color
	accent     tcell.Color
}

// ThemeTerminal keeps the terminal's own background, so a translucent or
// themed terminal stays itself. Only the foreground is ours.
var ThemeTerminal = Theme{
	background: tcell.ColorDefault,
	text:       tcell.ColorDefault,
	dim:        tcell.NewHexColor(0x6e7681),
	border:     tcell.NewHexColor(0x6e7681),
	title:      tcell.NewHexColor(0xf7ac16),
	accent:     tcell.NewHexColor(0x58a6ff),
}

// ThemeDark paints its own background, for a terminal whose colours fight the
// display.
var ThemeDark = Theme{
	background: tcell.NewHexColor(0x0f1319),
	text:       tcell.NewHexColor(0xc9d1d9),
	dim:        tcell.NewHexColor(0x6e7681),
	border:     tcell.NewHexColor(0x30363d),
	title:      tcell.NewHexColor(0xf7ac16),
	accent:     tcell.NewHexColor(0x58a6ff),
}

// ThemeNamed resolves a theme name, falling back to the terminal's own.
func ThemeNamed(name string) Theme {
	if name == "dark" {
		return ThemeDark
	}
	return ThemeTerminal
}

var theme = ThemeTerminal

// applyTheme sets the palette every primitive inherits.
func applyTheme() {
	tview.Styles.PrimitiveBackgroundColor = theme.background
	tview.Styles.ContrastBackgroundColor = theme.border
	tview.Styles.MoreContrastBackgroundColor = theme.accent
	tview.Styles.PrimaryTextColor = theme.text
	tview.Styles.SecondaryTextColor = theme.dim
	tview.Styles.TertiaryTextColor = theme.dim
	tview.Styles.BorderColor = theme.border
	tview.Styles.TitleColor = theme.title
	tview.Styles.GraphicsColor = theme.border
	tview.Styles.InverseTextColor = theme.background
}

// spacer is an empty region that still paints.
//
// A nil flex item reserves space without drawing anything, so the terminal
// background shows through it. Around a centred dialog that is the whole
// margin.
func spacer() *tview.Box {
	return tview.NewBox().SetBackgroundColor(theme.background)
}

// Focus markers for form fields.
//
// A field whose background matches the page has no highlight to show it has
// the keyboard, and the form's colours are uniform across items - Form.Draw
// pushes one style onto all of them every frame - so the cue has to live in
// the label text instead.
const (
	focusMark = "> "
	blurMark  = "  "
)

func focusedLabel(name string) string { return focusMark + name + "  " }
func blurredLabel(name string) string { return blurMark + name + "  " }

// markFocus makes a form item show when it holds the keyboard.
func markFocus(box *tview.Box, name string, setLabel func(string)) {
	box.SetFocusFunc(func() { setLabel(focusedLabel(name)) })
	box.SetBlurFunc(func() { setLabel(blurredLabel(name)) })
}
