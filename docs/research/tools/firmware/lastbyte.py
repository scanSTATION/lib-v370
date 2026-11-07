import sys,struct
files=sys.argv[1:]
D=[open(f,'rb').read() for f in files]
def cands(d):
    pl=d[256:65792]; whole=d[:65792]
    c={}
    for nm,r in (('pl',pl),('all',whole),('hdr',d[:256])):
        s=sum(r); x=0
        for b in r: x^=b
        c[nm+'sum']=s&0xff; c[nm+'neg']=(-s)&0xff; c[nm+'not']=(~s)&0xff; c[nm+'xor']=x
        w=sum(struct.unpack('<%dH'%(len(r)//2),r)); c[nm+'w16lo']=w&0xff; c[nm+'w16hi']=(w>>8)&0xff
        c[nm+'sum16hi']=(s>>8)&0xff
        c[nm+'sum24']=(s>>16)&0xff
    return c
C=[cands(d) for d in D]
for k in C[0]:
    if all(C[i][k]==D[i][65792] for i in range(len(D))): print('MATCH',k)
print('no simple match' )
# CRC8 brute force on dd payload, then confirm on others
def crc8(data,poly,init,refl):
    t=[]
    for i in range(256):
        if refl:
            r=i
            rp=int('{:08b}'.format(poly)[::-1],2)
            for _ in range(8): r=(r>>1)^rp if r&1 else r>>1
        else:
            r=i
            for _ in range(8): r=((r<<1)^poly)&0xff if r&0x80 else (r<<1)&0xff
        t.append(r)
    c=init
    for b in data: c=t[c^b]
    return c
hits=[]
for rng in ('pl',):
  for poly in range(1,256,2):
    for init in (0,0xff):
      for refl in (0,1):
        v=crc8(D[0][256:65792],poly,init,refl)
        for xo in (0,0xff):
          if v^xo==D[0][65792]: hits.append((poly,init,refl,xo))
print('crc8 cand on first file:',len(hits))
ok=[h for h in hits if all(crc8(d[256:65792],h[0],h[1],h[2])^h[3]==d[65792] for d in D[1:])]
print('crc8 confirmed on all:',ok)
