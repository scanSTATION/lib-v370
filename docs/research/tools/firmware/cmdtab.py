"""Flatten the USB I/O sequence of every dispatched command handler.
Usage: python3 -I cmdtab.py OUTPREFIX   (needs OUTPREFIX.pkl from fwan.py)
Events per function in address order (approximate: ignores branch structure),
callees expanded up to depth 3. SEND/RECV lengths are the r13 constant at the call."""
import sys, pickle, bisect
SEND, RECV, ACK, NAK = 0x2003d8c, 0x2003e40, 0x20077b8, 0x20077ce
P = pickle.load(open(sys.argv[1] + '.pkl', 'rb'))
starts = P['starts']; events = P['events']
def fend(s):
    j = bisect.bisect_right(starts, s)
    return starts[j] if j < len(starts) else s + 0x400
byf = {}
for pc, tgt, r12, r13 in events:
    j = bisect.bisect_right(starts, pc) - 1
    byf.setdefault(starts[j], []).append((pc, tgt, r12, r13))
def v(x): return '?' if x is None else (x if isinstance(x, str) else '%x' % x)
def flat(f, depth, seen):
    out = []
    for pc, tgt, r12, r13 in byf.get(f, []):
        if tgt == ACK: out.append('ACK')
        elif tgt == NAK: out.append('NAK')
        elif tgt == SEND: out.append('TX[%s]%s' % (v(r13), '' if r12 is None else '@' + v(r12)))
        elif tgt == RECV: out.append('RX[%s]%s' % (v(r13), '' if r12 is None else '@' + v(r12)))
        elif depth > 0 and tgt not in seen:
            sub = flat(tgt, depth - 1, seen | {tgt})
            if sub: out.append('%x{%s}' % (tgt, ' '.join(sub)))
    return out
def stub_target(e):
    # the stub at e calls the handler: first call event in [e, e+0x10)
    for pc, tgt, r12, r13 in events:
        if e <= pc < e + 0x10 and tgt not in (SEND, RECV, ACK, NAK): return tgt
    return None
names = {0x5638: '1b main', 0x57f4: '1e main', 0x7e2c: 'busy-1e', 0x8064: 'busy2-1b', 0x815c: 'busy2-1e'}
for (pc, toff, b, n, ents) in P['tables']:
    if toff not in names: continue
    import collections
    dflt = collections.Counter(ents).most_common(1)[0][0]
    print('== table %s @payload 0x%x (dispatch 0x%x) default=%x' % (names[toff], toff, pc, dflt))
    for j, e in enumerate(ents):
        if e == dflt: continue
        h = stub_target(e)
        seq = flat(e, 3, set()) if h is None else (flat(e, 0, set()) + ['%x{%s}' % (h, ' '.join(flat(h, 3, {h})))])
        print('  %02x stub %x handler %s : %s' % (j + b, e, v(h), ' '.join(seq)))
