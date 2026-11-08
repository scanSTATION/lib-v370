package v370

import (
	"context"
	"encoding/binary"
	"fmt"
)

// cmdMemory is `1e 83`: memory read (firmware handler 0x02006A40). After
// the ACK it takes 8 parameter bytes — type, u32 address, u24 length (all
// little endian) — and type 8 sends the bytes at the address directly
// (USB send routine 0x02003D8C), writing nothing. Other types copy through
// a RAM buffer or send ACKs; they are not used.
var cmdMemory = []byte{0x1e, 0x83}

const memDirect = 0x08

// MaxMemoryRead bounds ReadMemory (one bulk transfer).
const MaxMemoryRead = 512

// memoryRanges are the areas ReadMemory may read: the CPU's internal RAM
// and the external RAM holding the firmware and its data. Register areas
// (0x37xxxx) are excluded — reads there may have side effects.
var memoryRanges = [][2]uint32{{0x000, 0x1000}, {0x02000000, 0x02010000}}

// ReadMemory reads n bytes of scanner RAM at addr (read-only `1e 83`
// type 8). Only RAM ranges are allowed. Never call it during a scan.
func (s *Scanner) ReadMemory(ctx context.Context, addr uint32, n int) ([]byte, error) {
	if n <= 0 || n > MaxMemoryRead {
		return nil, fmt.Errorf("v370: memory read of %d bytes (1–%d)", n, MaxMemoryRead)
	}
	ok := false
	for _, r := range memoryRanges {
		if addr >= r[0] && uint64(addr)+uint64(n) <= uint64(r[1]) {
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("v370: memory read 0x%x+%d outside RAM", addr, n)
	}
	p := make([]byte, 8)
	p[0] = memDirect
	binary.LittleEndian.PutUint32(p[1:], addr)
	p[5], p[6], p[7] = byte(n), byte(n>>8), byte(n>>16)
	r, err := s.c.commandQuery(ctx, cmdMemory, p, n)
	if err != nil {
		return nil, fmt.Errorf("v370: read memory 0x%x: %w", addr, err)
	}
	return r, nil
}
