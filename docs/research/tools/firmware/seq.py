import sys,gzip,json,base64
# compact OUT/IN sequence; long data abbreviated
f=sys.argv[1]; n=int(sys.argv[2]) if len(sys.argv)>2 else 400
xs=[json.loads(l) for l in gzip.open(f,'rt')]
xs=[r for r in xs if r.get('kind')=='transfer' and r.get('type')=='bulk']
t0=xs[0].get('time_us',0); out=[]
for r in xs:
    d=base64.b64decode(r.get('data') or '')
    s=d.hex() if len(d)<=12 else '%s..(%d)'%(d[:6].hex(),len(d))
    out.append(('>' if r.get('ep')==2 else '<')+s)
    if len(out)>=n: break
# collapse repeats
res=[];prev=None;cnt=0
for o in out:
    res.append(o)
print(' '.join(res))
