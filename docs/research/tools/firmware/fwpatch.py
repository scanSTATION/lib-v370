"""Apply byte patches to a COPY of esfwdd.bin and report checksum/SHA-256.
Usage: python3 -I fwpatch.py IN.bin OUT.bin PATCH [PATCH...]
PATCH = ADDR=NEWHEX[/OLDHEX]
  ADDR: load address (0x0200xxxx), or payload offset with prefix 'p' (p0x55a7), or file offset with 'f'.
  NEWHEX: bytes to write (as they appear in the file, little-endian halfwords!).
  OLDHEX: optional expected current bytes; the patch is refused if they differ.
Writes to free space (payload >= 0xC9D4) must find 0xFF there.
Refuses to touch the 256-byte header and the trailing byte."""
import sys, hashlib
HDR, PL, BASE = 256, 65536, 0x2000000
def off(a):
    if a.startswith('p'): return int(a[1:], 0) + HDR
    if a.startswith('f'): return int(a[1:], 0)
    v = int(a, 0)
    if not BASE <= v < BASE + PL: raise SystemExit('address outside image: ' + a)
    return v - BASE + HDR
def main():
    src, dst, pats = sys.argv[1], sys.argv[2], sys.argv[3:]
    b = bytearray(open(src, 'rb').read())
    if len(b) != 65793 or b[:4] != b'EDLA': raise SystemExit('not an esfwdd.bin-style file')
    s0 = sum(b[HDR:HDR+PL]) & 0xff
    for p in pats:
        a, v = p.split('=')
        new, _, old = v.partition('/')
        o = off(a); n = bytes.fromhex(new)
        if not (HDR <= o and o + len(n) <= HDR + PL): raise SystemExit('outside payload: ' + p)
        cur = bytes(b[o:o+len(n)])
        if old and cur != bytes.fromhex(old): raise SystemExit('%s: found %s, expected %s' % (a, cur.hex(), old))
        if o - HDR >= 0xC9D4 and cur != b'\xff' * len(n) and not old:
            raise SystemExit('%s: free space is not 0xFF (%s)' % (a, cur.hex()))
        b[o:o+len(n)] = n
        print('patched file 0x%05x payload 0x%04x load 0x%08x: %s -> %s' % (o, o-HDR, o-HDR+BASE, cur.hex(), n.hex()))
    s1 = sum(b[HDR:HDR+PL]) & 0xff
    open(dst, 'wb').write(b)
    print('sum8 %02x -> %02x (the driver computes it; nothing else to fix)' % (s0, s1))
    print('sha256', hashlib.sha256(b).hexdigest())
if __name__ == '__main__':
    main()
