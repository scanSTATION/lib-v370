package v370

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// Generated tables: some table uploads are standard curves that the driver
// computes itself. A profile write step names such a table (Step.Gen)
// instead of carrying its bytes; ParseProfile fills in the bytes.
const (
	// GenGamma18 is the 12-bit tone curve for gamma 1.8: 4096 little-endian
	// uint16 values, a 16-bit curve reduced to 12 bits.
	GenGamma18 = "gamma-1.8"
	// GenRamp8 is the 8-bit identity table 0, 1, …, 255.
	GenRamp8 = "ramp-8"
)

// generators maps table names to their builders, in matching order.
var generators = []struct {
	name  string
	build func() []byte
}{
	{GenGamma18, gamma18},
	{GenRamp8, ramp8},
}

// GenerateTable returns the bytes of a named generated table.
func GenerateTable(name string) ([]byte, error) {
	for _, g := range generators {
		if g.name == name {
			return g.build(), nil
		}
	}
	return nil, fmt.Errorf("unknown generated table %q", name)
}

// generatedName returns the name of the generated table equal to b, or "".
func generatedName(b []byte) string {
	for _, g := range generators {
		if bytes.Equal(b, g.build()) {
			return g.name
		}
	}
	return ""
}

func gamma18() []byte {
	b := make([]byte, 4096*2)
	for i := range 4096 {
		v := int(65535*math.Pow(float64(i)/4095, 1/1.8)) >> 4
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

func ramp8() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// MarkGenerated names every write step whose bytes equal a generated table
// (the bytes stay in place; Compact drops them).
func (p *Profile) MarkGenerated() {
	for i := range p.Steps {
		if s := &p.Steps[i]; s.Kind == StepWrite && s.Gen == "" && len(s.Data) >= 256 {
			s.Gen = generatedName(s.Data)
		}
	}
}

// Compact drops the bytes of named generated tables before a profile is
// stored; ParseProfile computes them again.
func (p *Profile) Compact() {
	for i := range p.Steps {
		if p.Steps[i].Gen != "" {
			p.Steps[i].Data = nil
		}
	}
}
