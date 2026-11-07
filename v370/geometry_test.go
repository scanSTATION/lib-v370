package v370_test

import (
	"testing"

	"github.com/scanSTATION/lib-v370/v370"
)

// Geometry of every built-in profile, as measured independently from the
// raw data (line period analysis, docs/protocol.md §8).
func TestProfileGeometry(t *testing.T) {
	want := map[string][4]int{ // dpi, width, lines, lines per block
		"a4-100dpi-color":        {100, 848, 1172, 20},
		"a4-200dpi-color":        {200, 1680, 2345, 10},
		"a4-300dpi-color":        {300, 2688, 3519, 6},
		"a4-300dpi-gray":         {300, 2688, 3519, 6},
		"a4-600dpi-color":        {600, 5008, 7040, 3},
		"user-148x210mm-1200dpi": {1200, 7056, 9969, 2},
		"user-100x50mm-300dpi":   {300, 1536, 603, 11},
		"user-50x50mm-600dpi":    {600, 1200, 1205, 14},
		"user-25x25mm-1200dpi":   {1200, 1200, 1229, 14},
	}
	for name, w := range want {
		p, err := v370.LoadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		g, err := p.Geometry()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := [4]int{g.DPI, g.Width, g.Lines, g.LinesPerBlock}; got != w {
			t.Errorf("%s: geometry %v, want %v", name, got, w)
		}
		// After channel alignment the height is exactly the requested one.
		if g.OutputHeight() != p.HeightPx {
			t.Errorf("%s: output height %d, requested %d", name, g.OutputHeight(), p.HeightPx)
		}
		if g.StreamSize() != p.ImageBytes {
			t.Errorf("%s: stream size %d, profile %d", name, g.StreamSize(), p.ImageBytes)
		}
	}
}
