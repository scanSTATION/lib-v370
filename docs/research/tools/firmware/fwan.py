"""Firmware map analyser for esfwdd.bin payload (S1C33, base 0x02000000).
Usage: python3 -I fwan.py PAYLOAD.bin OUTPREFIX
Writes OUTPREFIX.funcs.txt (per-function summary), OUTPREFIX.tables.txt (jump tables),
OUTPREFIX.ram.txt (RAM xref), OUTPREFIX.io.txt (peripheral xref), OUTPREFIX.ann.asm (annotated asm).
Static analysis only; heuristic constant tracking inside straight-line code."""
import sys, os, struct, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import s1c33dis as S

BASE = 0x2000000
R8 = 0x200
SEND, RECV, ACK, NAK = 0x2003d8c, 0x2003e40, 0x20077b8, 0x20077ce
CODE_END = 0xC9D4

def u32(d, o): return struct.unpack_from('<I', d, o)[0]

def fmtv(v):
    return '?' if v is None else (v if isinstance(v,str) else hex(v))

def main():
    d = open(sys.argv[1], 'rb').read()
    out = sys.argv[2]
    # payload 0x400..0x7d0 is copied to internal RAM 0x4a0 at start-up and runs there
    L = S.run(d, 0, 0x400, BASE) + S.run(d, 0x400, 0x7d0, 0x4a0 - 0x400) + S.run(d, 0x7d0, CODE_END, BASE)
    idx = {pc: i for i, (pc, w, t, info, x) in enumerate(L)}
    # ---- jump tables
    tables = []
    for k in range(len(L) - 3):
        pc, w, t, info, x = L[k]
        if info and info[0] == 'imm' and info[1] == 'ld.w' and BASE <= info[3] < BASE + 0x10000:
            nxt = L[k+1:k+4]
            if any(n[3] and n[3][0] == 'ind' and n[3][1] == 'jp' for n in nxt):
                # find cmp and sub before
                prev = L[max(0, k-12):k]
                cmps = [p[3][3] for p in prev if p[3] and p[3][0] == 'imm' and p[3][1] == 'cmp']
                subs = [p[3][3] for p in prev if p[3] and p[3][0] == 'imm' and p[3][1] == 'sub']
                n = (cmps[-1] + 1) if cmps else 0
                b = subs[-1] if subs else 0
                toff = info[3] - BASE
                ents = [u32(d, toff + 4*j) for j in range(n)]
                tables.append((pc, toff, b, n, ents))
    # ---- function starts
    calls = collections.defaultdict(set)
    for pc, w, t, info, x in L:
        if info and info[0] == 'br' and info[1] == 'call':
            calls[info[2]].add(pc)
    starts = set(a for a in calls if BASE <= a < BASE + CODE_END or 0x4a0 <= a < 0x870)
    tabtargets = {}
    for (pc, toff, b, n, ents) in tables:
        cnt = collections.Counter(ents)
        dflt = cnt.most_common(1)[0][0] if ents else None
        for j, e in enumerate(ents):
            tabtargets.setdefault(e, []).append((toff, j + b, e == dflt))
    # ISR / vector targets
    for o in range(0, 0x200, 4):
        v = u32(d, o)
        if BASE <= v < BASE + CODE_END: starts.add(v)
    starts.add(0x200140c)
    starts.add(0x4a0)
    S_sorted = sorted(starts)
    # ---- const tracking, xrefs
    labels = set()
    for pc, w, t, info, x in L:
        if info and info[0] == 'br': labels.add(info[2])
    for e in tabtargets: labels.add(e)
    reg = {}
    ram = collections.defaultdict(lambda: collections.defaultdict(set))  # addr -> kind -> pcs
    io = collections.defaultdict(lambda: collections.defaultdict(set))
    rom = collections.defaultdict(set)
    sends = []
    events = []
    ann = {}
    def classify(a):
        if a < 0x1000: return 'ram'
        if 0x2000000 <= a < 0x2010000: return 'rom'
        if 0x300000 <= a < 0x400000 or 0x40000 <= a < 0x60000: return 'io'
        return None
    for i, (pc, w, t, info, x) in enumerate(L):
        if pc in labels: reg = {}
        note = ''
        if info:
            k = info[0]
            if k == 'imm':
                op, rd, v = info[1], info[2], info[3]
                if op == 'ld.w':
                    reg[rd] = v
                    c = classify(v)
                    if c == 'rom': rom[v].add(pc)
                    if c == 'io': io[v]['addr'].add(pc); note = 'IO 0x%x' % v
                elif rd in reg and isinstance(reg[rd], int) and op in ('add', 'sub', 'and', 'or', 'xor'):
                    a = reg[rd]
                    reg[rd] = {'add': a+v, 'sub': a-v, 'and': a & v, 'or': a | v, 'xor': a ^ v}[op] & 0xffffffff
                elif op != 'cmp':
                    reg.pop(rd, None)
            elif k in ('mem', 'bit'):
                if k == 'mem': _, dirn, a, imm, b = info; op = dirn
                else: _, op, a, imm, bit = info; dirn = 'st' if op in ('bclr', 'bset', 'bnot') else 'ld'
                base = R8 if a == 8 else reg.get(a)
                if isinstance(base, str): base = None
                if base is not None:
                    ea = (base + (imm or 0)) & 0xffffffff
                    c = classify(ea)
                    tag = 'w' if dirn == 'st' else 'r'
                    if k == 'bit': tag = '%s%d' % (op, bit)
                    if c == 'ram': ram[ea][tag].add(pc); note = 'RAM 0x%x %s' % (ea, tag)
                    elif c == 'io': io[ea][tag].add(pc); note = 'IO 0x%x %s' % (ea, tag)
                    elif c == 'rom': rom[ea].add(pc)
                if k == 'mem' and dirn == 'ld': reg.pop(b, None)
            elif k == 'alu':
                op, rd, rs = info[1], info[2], info[3]
                if op == 'ld.w' and rs in reg: reg[rd] = reg[rs]
                elif op != 'cmp': reg.pop(rd, None)
            elif k == 'br' and info[1] == 'call':
                tgt = info[2]
                if tgt in (SEND, RECV):
                    sends.append((pc, 'send' if tgt == SEND else 'recv', reg.get(12), reg.get(13)))
                    note = '%s(buf=%s,len=%s)' % ('SEND' if tgt == SEND else 'RECV',
                        fmtv(reg.get(12)), fmtv(reg.get(13)))
                elif tgt == ACK: note = 'ACK'
                elif tgt == NAK: note = 'NAK'
                events.append((pc, tgt, reg.get(12), reg.get(13)))
                for r in list(reg):
                    if r not in (0, 1, 2, 3, 8): reg.pop(r)
            elif k in ('ret', 'ind'):
                reg = {}
        else:
            # unknown-effect instruction: clobber its destination (low 4 bits) conservatively
            if t.startswith('ld.w %r') and t.endswith(',%sp'):
                reg[w & 15] = 'sp'
            elif not t.startswith(('nop', 'pushn', 'popn', 'ext', 'add %sp', 'sub %sp')) and (w >> 13) in (2, 4, 5):
                reg.pop(w & 15, None)
        if note: ann[pc] = note
    # ---- per function
    def func_of(pc):
        import bisect
        j = bisect.bisect_right(S_sorted, pc) - 1
        return S_sorted[j] if j >= 0 else None
    fsum = collections.defaultdict(lambda: {'ram': set(), 'io': set(), 'calls': set(), 'io_ops': set(), 'sr': []})
    for a, kinds in ram.items():
        for tag, pcs in kinds.items():
            for p in pcs: fsum[func_of(p)]['ram'].add((a, tag))
    for a, kinds in io.items():
        for tag, pcs in kinds.items():
            for p in pcs: fsum[func_of(p)]['io'].add((a, tag))
    for pc, w, t, info, x in L:
        if info and info[0] == 'br' and info[1] == 'call':
            fsum[func_of(pc)]['calls'].add(info[2])
    for (pc, kind, b, n) in sends:
        fsum[func_of(pc)]['sr'].append((kind, b, n, pc))
    with open(out + '.funcs.txt', 'w') as f:
        for s in S_sorted:
            fs = fsum.get(s)
            callers = sorted(calls.get(s, ()))
            po = s - BASE if s >= BASE else s - 0xa0
            f.write('FUNC 0x%08x (payload 0x%04x, file 0x%04x) callers=%d %s\n' % (s, po, po+256, len(callers),
                    ' '.join('%x' % c for c in callers[:12]) + (' ...' if len(callers) > 12 else '')))
            if s in tabtargets: f.write('  table-entry: %s\n' % ', '.join('tbl@%x case 0x%x%s' % (a, c, ' (default)' if df else '') for a, c, df in tabtargets[s][:6]))
            if not fs: continue
            if fs['sr']: f.write('  io: %s\n' % ' '.join('%s(buf=%s,len=%s)@%x' % (k, fmtv(b), fmtv(n), p) for k, b, n, p in fs['sr']))
            if fs['calls']: f.write('  calls: %s\n' % ' '.join('%x' % c for c in sorted(fs['calls'])))
            if fs['ram']: f.write('  ram: %s\n' % ' '.join('%x:%s' % (a, t) for a, t in sorted(fs['ram'])))
            if fs['io']: f.write('  periph: %s\n' % ' '.join('%x:%s' % (a, t) for a, t in sorted(fs['io'])))
    with open(out + '.tables.txt', 'w') as f:
        for (pc, toff, b, n, ents) in tables:
            cnt = collections.Counter(ents); dflt = cnt.most_common(1)[0][0] if ents else 0
            f.write('TABLE dispatch@%x table payload 0x%x base=0x%x n=%d default=%x\n' % (pc, toff, b, n, dflt))
            for j, e in enumerate(ents):
                if e != dflt: f.write('  case 0x%02x -> %x\n' % (j + b, e))
    with open(out + '.ram.txt', 'w') as f:
        for a in sorted(ram):
            f.write('RAM 0x%03x: %s\n' % (a, '; '.join('%s: %s' % (t, ' '.join('%x' % func_of(p) for p in sorted(set(func_of(q) for q in ps))))
                                                    for t, ps in sorted(ram[a].items()))))
    with open(out + '.io.txt', 'w') as f:
        for a in sorted(io):
            f.write('IO 0x%06x: %s\n' % (a, '; '.join('%s: %s' % (t, ' '.join('%x' % q for q in sorted(set(func_of(p) for p in ps))))
                                                    for t, ps in sorted(io[a].items()))))
    with open(out + '.ann.asm', 'w') as f:
        for pc, w, t, info, x in L:
            pre = ''
            if pc in starts: pre = '\n; ---- FUNC %08x  callers=%d\n' % (pc, len(calls.get(pc, ())))
            if pc in tabtargets: pre += '; TABLE TARGET %s\n' % ', '.join('tbl@%x case 0x%x' % (a, c) for a, c, df in tabtargets[pc][:4] if not df)
            f.write('%s%08x: %04x  %-34s%s\n' % (pre, pc, w, t, ('; ' + ann[pc]) if pc in ann else ''))
    with open(out + '.rom.txt', 'w') as f:
        for a in sorted(rom):
            if a in starts: kind = 'code(func)'
            elif a in labels: kind = 'code(label)'
            else: kind = 'DATA?'
            f.write('ROM 0x%08x payload 0x%04x %-11s refs: %s\n' % (a, a - BASE, kind, ' '.join('%x(f%x)' % (p, func_of(p)) for p in sorted(rom[a]))))
    import pickle
    pickle.dump({'events': events, 'starts': S_sorted, 'tables': tables, 'calls': {k: sorted(v) for k, v in calls.items()}}, open(out + '.pkl', 'wb'))
    print('functions', len(S_sorted), 'tables', len(tables), 'ram vars', len(ram), 'io addrs', len(io), 'send/recv', len(sends))

if __name__ == '__main__':
    main()
