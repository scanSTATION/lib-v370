"""Print parameter blocks of selected commands from v370 profile JSONs.
Usage: python3 -I profparams.py CMDHEX[,CMDHEX..] profile.json..."""
import sys, json, base64
cmds = [bytes.fromhex(c) for c in sys.argv[1].split(',')]
for f in sys.argv[2:]:
    st = json.load(open(f))['steps']
    w = [base64.b64decode(s['d']) for s in st if s['k'] == 'write']
    for i, d in enumerate(w):
        if d in cmds and i + 1 < len(w):
            print(f.split('/')[-1][:24], d.hex(), w[i+1].hex() if len(w[i+1]) < 64 else '(%d B)' % len(w[i+1]))
