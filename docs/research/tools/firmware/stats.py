import sys, math, collections
for p in sys.argv[1:]:
    d=open(p,'rb').read(); pl=d[256:65792]; last=d[65792]
    s=sum(pl)&0xff; x=0
    for b in pl: x^=b
    s16=sum(pl)&0xffff
    print(p.split('/')[-1], 'sum8=%02x xor=%02x sum16=%04x last=%02x neg=%02x ~sum=%02x sumall=%02x'%(s,x,s16,last,(-s)&0xff,(~s)&0xff,sum(d[:65792])&0xff))
    ent=[]
    for i in range(0,65536,4096):
        c=collections.Counter(pl[i:i+4096]); n=4096
        ent.append(round(-sum(v/n*math.log2(v/n) for v in c.values()),2))
    print('  entropy/4K:',ent)
    c=collections.Counter(pl); print('  top bytes:',[(hex(k),v) for k,v in c.most_common(12)])
    # trailing FF/00 padding
    j=len(pl)
    while j>0 and pl[j-1] in (0xff,): j-=1
    k=len(pl)
    while k>0 and pl[k-1]==0: k-=1
    print('  last non-FF payload off=%x last non-00=%x'%(j,k))
