import os, sys; sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
"""Find code references to a 32-bit constant (address) built via ext+ld.w, and scan ramp tables."""
import sys, struct
import s1c33dis as D
d=open('payload.bin','rb').read()
BASE=0x2000000

def all_imm_consts():
    """Return list of (pc, value) for every imm/ld.w immediate decoded."""
    res=[]
    for pc,w,txt,info,ext in D.run(d,0,0xca00,BASE):
        if info and info[0]=='imm' and info[1]=='ld.w':
            res.append((pc,info[3],txt))
        if info and info[0]=='mem':
            pass
    return res

if __name__=='__main__':
    consts=all_imm_consts()
    # references into table regions of interest
    for lo,hi,name in [(0x2005300,0x2005480,'ramp-region'),
                       (0x2005400,0x2005478,'ramp5400'),
                       (0x2005638,0x2005730,'1b-table'),
                       (0x2006f00,0x2006f10,'id-handler')]:
        hits=[(hex(pc),hex(v)) for pc,v,t in consts if lo<=v<hi]
        print('%-14s refs:'%name, hits[:20])
    # Scan whole file for decreasing u16 ramps (len>=16): start>=1000, monotonic non-increasing for >=16 steps
    print('--- candidate ramp tables (u16 LE, >=16 monotone-decreasing steps, start>800) ---')
    i=0x1000  # skip vector/startup
    n=len(d)
    off=0x4000
    while off < 0xca00-2:
        # try to grow a non-increasing run
        start=off
        prev=struct.unpack('<H',d[off:off+2])[0]
        run=1; o=off+2
        while o<0xca00-2:
            v=struct.unpack('<H',d[o:o+2])[0]
            if v<=prev and v>0 and prev<60000:
                prev=v; run+=1; o+=2
            else:
                break
        if run>=16 and struct.unpack('<H',d[start:start+2])[0]>800:
            first=struct.unpack('<H',d[start:start+2])[0]
            last=struct.unpack('<H',d[start+2*(run-1):start+2*run])[0]
            print('  0x%x len=%d first=%d last=%d'%(start,run,first,last))
            off=start+2*run
        else:
            off+=2
