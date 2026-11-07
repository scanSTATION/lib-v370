package trace

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *Trace {
	return &Trace{
		Meta:        Meta{VendorID: 0x04b8, ProductID: 0x014a, Scenario: []string{"--dpi", "300"}},
		Descriptors: []Descriptor{{Value: 0x0100, Data: []byte{18, 1, 0, 2}}},
		Transfers: []Transfer{
			{Seq: 1, Type: TypeControl, Setup: []byte{0x80, 6, 0, 1, 0, 0, 18, 0}, Data: []byte{18, 1}},
			{Seq: 2, Type: TypeBulk, Endpoint: 0x02, Data: []byte{0x1b, 'F'}},
			{Seq: 3, Type: TypeBulk, Endpoint: 0x81, Data: []byte{2, 0}, TimeUS: 42},
			{Seq: 4, Type: TypeBulk, Endpoint: 0x81, Status: -2},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	for _, name := range []string{"t.jsonl", "t.jsonl.gz"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := sample().Save(path); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			want := sample()
			if len(got.Transfers) != len(want.Transfers) || len(got.Descriptors) != 1 {
				t.Fatalf("got %d transfers %d descriptors", len(got.Transfers), len(got.Descriptors))
			}
			for i, tr := range got.Transfers {
				w := want.Transfers[i]
				if tr.Seq != w.Seq || tr.Type != w.Type || tr.Endpoint != w.Endpoint ||
					!bytes.Equal(tr.Data, w.Data) || !bytes.Equal(tr.Setup, w.Setup) || tr.Status != w.Status || tr.TimeUS != w.TimeUS {
					t.Errorf("transfer %d: got %+v want %+v", i, tr, w)
				}
			}
			if !bytes.Equal(got.Descriptors[0].Data, want.Descriptors[0].Data) || got.Descriptors[0].Value != 0x0100 {
				t.Errorf("descriptor mismatch: %+v", got.Descriptors[0])
			}
			if got.Meta.ProductID != 0x014a || len(got.Meta.Scenario) != 2 {
				t.Errorf("meta mismatch: %+v", got.Meta)
			}
		})
	}
}

func TestReadRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"unknown kind":   `{"kind":"bogus"}`,
		"unknown field":  `{"kind":"transfer","seq":1,"type":"bulk","ep":2,"evil":1}`,
		"bad endpoint":   `{"kind":"transfer","seq":1,"type":"bulk","ep":0}`,
		"short setup":    `{"kind":"transfer","seq":1,"type":"control","setup":"AAE="}`,
		"bad type":       `{"kind":"transfer","seq":1,"type":"iso","ep":1}`,
		"duplicate meta": `{"kind":"meta","meta":{}}` + "\n" + `{"kind":"meta","meta":{}}`,
		"bad descriptor": `{"kind":"descriptor","value":256,"data":"AA=="}`,
		"not json":       `hello`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Read(strings.NewReader(in)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestIsIn(t *testing.T) {
	if !(Transfer{Type: TypeControl, Setup: []byte{0xc0, 0, 0, 0, 0, 0, 0, 0}}).IsIn() {
		t.Error("vendor IN control should be IN")
	}
	if (Transfer{Type: TypeBulk, Endpoint: 0x02}).IsIn() {
		t.Error("ep 0x02 should be OUT")
	}
}
