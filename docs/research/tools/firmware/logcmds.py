import sys,gzip,json,base64
# print 1e 8e / 1e 8f / 1e 93 exchanges (cmd + next OUT param + replies) from a trace
for f in sys.argv[1:]:
    recs=[json.loads(l) for l in gzip.open(f,'rt')]
    xs=[r for r in recs if r.get('kind')=='transfer' and r.get('type')=='bulk']
    out=[]
    for i,r in enumerate(xs):
        d=base64.b64decode(r.get('data','') or '')
        ep=r.get('endpoint',r.get('ep'))
        if len(d)==2 and d[0]==0x1e and d[1] in (0x8e,0x8f,0x93,0x85):
            seq=[]
            for r2 in xs[i:i+4]:
                d2=base64.b64decode(r2.get('data','') or '')
                seq.append('%s:%s'%(r2.get('endpoint',r2.get('ep','?')),d2[:8].hex()))
            out.append(' '.join(seq))
    print('==',f.split('/')[-1],len(out))
    for o in out: print('  ',o)
