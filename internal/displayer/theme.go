package displayer

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Explicit colours rather than the terminal palette.
//
// tview defaults to tcell.ColorBlack, which resolves to whatever the terminal
// calls colour 0. That is rarely the terminal's actual background: Ubuntu's is
// a dark purple, so every cell tview painted came out near-black against a
// purple surround wherever it painted nothing.
var theme = struct {
	background tcell.Color
	text       tcell.Color
	dim        tcell.Color
	border     tcell.Color
	title      tcell.Color
	accent     tcell.Color
}{
	background: tcell.NewHexColor(0x0f1319),
	text:       tcell.NewHexColor(0xc9d1d9),
	dim:        tcell.NewHexColor(0x6e7681),
	border:     tcell.NewHexColor(0x30363d),
	title:      tcell.NewHexColor(0xf7ac16),
	accent:     tcell.NewHexColor(0x58a6ff),
}

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
