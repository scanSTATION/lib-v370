package v370

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestGamma18Table(t *testing.T) {
	b, err := GenerateTable(GenGamma18)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 8192 {
		t.Fatalf("len %d", len(b))
	}
	prev := -1
	for i := range 4096 {
		v := int(binary.LittleEndian.Uint16(b[2*i:]))
		if v < prev || v > 4095 {
			t.Fatalf("entry %d = %d after %d", i, v, prev)
		}
		prev = v
	}
	if first, mid, last := binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2*2048:]), binary.LittleEndian.Uint16(b[2*4095:]); first != 0 || mid != 2787 || last != 4095 {
		t.Fatalf("0/2048/4095 = %d/%d/%d", first, mid, last)
	}
	// SHA-256 of the tone curve observed in every recorded scan.
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != "654c89944727ea319602a7b24d04451571d50ae4f3bf803c06f27df69a791e49" {
		t.Fatalf("sha256 %s", got)
	}
}

func TestRamp8Table(t *testing.T) {
	b, err := GenerateTable(GenRamp8)
	if err != nil || len(b) != 256 || b[0] != 0 || b[200] != 200 || b[255] != 255 {
		t.Fatalf("ramp: %v len %d", err, len(b))
	}
	if _, err := GenerateTable("nope"); err == nil {
		t.Fatal("unknown table accepted")
	}
}

// Built-in profiles store generated tables by name only.
func TestBuiltinProfilesUseGeneratedTables(t *testing.T) {
	for _, name := range ProfileNames() {
		raw, err := profileFS.ReadFile("profiles/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var stored Profile
		if err := json.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		p, err := LoadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		gen := 0
		for i, s := range stored.Steps {
			if s.Gen == "" {
				continue
			}
			gen++
			if len(s.Data) != 0 {
				t.Errorf("%s step %d: generated table %q stored with its bytes", name, i, s.Gen)
			}
			if len(p.Steps[i].Data) == 0 {
				t.Errorf("%s step %d: generated table %q not filled in", name, i, s.Gen)
			}
		}
		if gen == 0 {
			t.Errorf("%s: no generated tables", name)
		}
		// Nothing generatable is stored as bytes.
		for i, s := range stored.Steps {
			if s.Kind == StepWrite && s.Gen == "" && generatedName(s.Data) != "" {
				t.Errorf("%s step %d: stores a generatable table as bytes", name, i)
			}
		}
	}
}

func TestProfileGeneratedTableErrors(t *testing.T) {
	base := `{"name":"x","steps":[%s,{"k":"data","n":4,"c":4,"t":"image"}]}`
	for _, tc := range []struct{ step, want string }{
		{`{"k":"write","g":"nope"}`, "unknown generated table"},
		{`{"k":"write","g":"ramp-8","d":"AAE="}`, "differs from generated table"},
	} {
		_, err := ParseProfile([]byte(strings.Replace(base, "%s", tc.step, 1)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.step, err, tc.want)
		}
	}
	p, err := ParseProfile([]byte(strings.Replace(base, "%s", `{"k":"write","g":"ramp-8"}`, 1)))
	if err != nil || len(p.Steps[0].Data) != 256 {
		t.Fatalf("ramp step: %v", err)
	}
	p.Compact()
	if p.Steps[0].Data != nil || p.Steps[0].Gen != GenRamp8 {
		t.Fatal("Compact kept the bytes")
	}
}
