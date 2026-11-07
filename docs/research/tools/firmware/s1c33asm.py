"""Tiny S1C33 encoder for patching esfwdd.bin (only what hooks need).
Usage examples (prints little-endian hex bytes):
  python3 -I s1c33asm.py call 0x0200c9e0 0x02005d00     # call TARGET placed at PC
  python3 -I s1c33asm.py jp   0x02005e38 0x0200c9f0
  python3 -I s1c33asm.py ldw  r12 0x0200c9e0             # ext;ext;ld.w %r12,imm32 (6 bytes)
  python3 -I s1c33asm.py ret | nop
Branch rule (verified against the disassembly): target = PC_of_branch_opcode + 2*disp,
disp = sign-extended (ext13<<8 | s8) with one ext (21 bit, +-2 MiB) - always emitted here.
The ext halfword sits BEFORE the branch opcode, so the branch opcode is at PC+2 when you pass the
address of the ext: this tool takes PC = address of the FIRST emitted halfword."""
import sys
BR = {'jrgt': 4, 'jrge': 5, 'jrlt': 6, 'jrle': 7, 'jrugt': 8, 'jruge': 9, 'jrult': 10, 'jrule': 11,
      'jreq': 12, 'jrne': 13, 'call': 14, 'jp': 15}
def hw(v): return bytes([v & 0xff, (v >> 8) & 0xff])
def branch(op, target, pc):
    bpc = pc + 2                     # opcode follows one ext
    d = target - bpc
    if d & 1: raise SystemExit('odd target')
    disp = d // 2
    if not -(1 << 20) <= disp < (1 << 20): raise SystemExit('out of range')
    disp &= (1 << 21) - 1
    ext = 0xC000 | (disp >> 8)
    opw = (BR[op] << 9) | (disp & 0xff)
    return hw(ext) + hw(opw)
def ldw(rd, imm):
    imm &= 0xffffffff
    e0 = 0xC000 | (imm >> 19); e1 = 0xC000 | ((imm >> 6) & 0x1fff)
    return hw(e0) + hw(e1) + hw(0x6C00 | ((imm & 0x3f) << 4) | rd)
if __name__ == '__main__':
    a = sys.argv[1:]
    if a[0] in BR: b = branch(a[0], int(a[1], 0), int(a[2], 0))
    elif a[0] == 'ldw': b = ldw(int(a[1].lstrip('%r')), int(a[2], 0))
    elif a[0] == 'ret': b = hw(0x0640)
    elif a[0] == 'nop': b = hw(0x0000)
    else: raise SystemExit(__doc__)
    print(b.hex())
