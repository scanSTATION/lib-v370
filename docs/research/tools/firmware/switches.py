import sys,os,struct,collections; sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
import s1c33dis as S
for f in sys.argv[1:]:
    d=open(f,'rb').read()
    rv=struct.unpack_from('<I',d,0)[0]
    L=S.run(d,0x1000,len(d)-2,0x2000000)
    print('==',f,'reset vector 0x%x'%rv, 'vec[0x1c]=0x%x'%struct.unpack_from('<I',d,0x1c)[0])
    for k in range(len(L)-8):
        i=L[k][3]
        if not(i and i[0]=='imm' and i[1]=='sub'): continue
        # look ahead for cmp, jrugt, table load, jp %r10
        win=L[k:k+12]
        cmpv=[x[3][3] for x in win if x[3] and x[3][0]=='imm' and x[3][1]=='cmp']
        tbl=[x[3][3] for x in win if x[3] and x[3][0]=='imm' and x[3][1]=='ld.w' and 0x2000000<=x[3][3]<0x2010000]
        jp=[x for x in win if x[3] and x[3][0]=='ind' and x[3][1]=='jp']
        if cmpv and tbl and jp:
            base=i[3]; n=cmpv[0]+1; t=tbl[0]-0x2000000
            w=[struct.unpack_from('<I',d,t+4*j)[0] for j in range(n)]
            dflt=collections.Counter(w).most_common(1)[0][0]
            # find prefix compare just before
            pre=[x[3][3] for x in L[k-12:k] if x[3] and x[3][0]=='imm' and x[3][1]=='cmp' and x[3][3] in (0x1b,0x1e)]
            cases=[j+base for j in range(n) if w[j]!=dflt]
            print('  switch@0x%x prefix=%s base=0x%x n=%d table=0x%x cases=%s'%(L[k][0],[hex(p) for p in pre],base,n,t,' '.join('%02x'%c for c in cases)))
