"""Minimal the vendor S1C33000 (C33 STD core) disassembler, written for static analysis.
Encoding per the vendor S1C33000 Core CPU Manual (16-bit fixed-length, little-endian).
Usage: python3 -I s1c33dis.py payload.bin [start] [end] [base]"""
import sys
def sx(v,b): return v-(1<<b) if v&(1<<(b-1)) else v
R=lambda n:'%%r%d'%n
SREG={0:'%psr',1:'%sp',2:'%alr',3:'%ahr'}
BR=['jrgt','jrge','jrlt','jrle','jrugt','jruge','jrult','jrule','jreq','jrne','call','jp']
MEM=['ld.b','ld.ub','ld.h','ld.uh','ld.w','ld.b','ld.h','ld.w']
ALU=['add','sub','cmp','ld.w','and','or','xor','not']
def dis(w,pc,ext):
    cls=w>>13
    if cls==0:
        op1=(w>>9)&0xf; d='.d' if w&0x100 else ''; op2=(w>>6)&3
        if op1>=4:
            s8=w&0xff
            if len(ext)==0: disp=sx(s8,8)
            elif len(ext)==1: disp=sx((ext[0]<<8)|s8,21)
            else: disp=sx((((ext[0]>>3)&0x3ff)<<21)|(ext[1]<<8)|s8,31)
            # S1C33: target = PC + disp*2 (sign8 is in halfword units)
            return '%s%s 0x%x'%(BR[op1-4],d,(pc+disp*2)&0xffffffff),('br',BR[op1-4],(pc+disp*2)&0xffffffff)
        if op1==0: return ['nop','slp','halt','?'][op2],None
        if op1==1: return ['pushn','popn','?','?'][op2]+' '+R(w&15),None
        if op1==2: return ['brk','retd','int %d'%(w&3),'reti'][op2],('ret',) if op2==3 else None
        if op1==3:
            m=['call','ret','jp','?'][op2]
            if op2==1: return 'ret'+d,('ret',)
            return '%s%s %s'%(m,d,R(w&15)),('ind',m)
        return '?',None
    if cls==1:
        op1=(w>>10)&7; op2=(w>>8)&3; a=(w>>4)&15; b=w&15
        if op2==2: return '%s %s,%s'%(ALU[op1],R(b),R(a)),('alu',ALU[op1],b,a)
        if op2 in (0,1):
            imm=None
            if len(ext)==1: imm=ext[0]
            elif len(ext)==2: imm=(ext[0]<<13)|ext[1]
            ea='[%s%s%s]'%(R(a),'+' if op2==1 else '','' if imm is None else '+0x%x'%imm)
            if op1<5: return '%s %s,%s'%(MEM[op1],R(b),ea),('mem','ld',a,imm,b)
            return '%s %s,%s'%(MEM[op1],ea,R(b)),('mem','st',a,imm,b)
        return '?',None
    if cls==2:
        op1=(w>>10)&7; imm=(w>>4)&0x3f; b=w&15
        sz=[1,1,2,2,4,1,2,4][op1]; off=imm*sz
        if ext: off=(ext[0] if len(ext)==1 else (ext[0]<<13|ext[1]))+imm*0  # ext form: [sp+imm19]
        ea='[%%sp+0x%x]'%off
        return ('%s %s,%s'%(MEM[op1],R(b),ea) if op1<5 else '%s %s,%s'%(MEM[op1],ea,R(b))),None
    if cls==3:
        op1=(w>>10)&7; i6=(w>>4)&0x3f; b=w&15
        signed=op1 in (2,3,4,5,6,7)
        if len(ext)==0: v=sx(i6,6) if signed else i6
        elif len(ext)==1: v=(ext[0]<<6)|i6; v=sx(v,19) if signed else v
        else: v=((ext[0]<<19)|(ext[1]<<6)|i6)&0xffffffff
        return '%s %s,0x%x'%(ALU[op1],R(b),v&0xffffffff),('imm',ALU[op1],b,v&0xffffffff)
    if cls==4:
        op1=(w>>10)&7; op2=(w>>8)&3
        if op1==0: return 'add %%sp,0x%x'%((w&0x3ff)*4),None
        if op1==1: return 'sub %%sp,0x%x'%((w&0x3ff)*4),None
        a=(w>>4)&15; b=w&15
        if op1>=2:
            n=['srl','sll','sra','sla','rr','rl'][op1-2]
            if op2==0: return '%s %s,%d'%(n,R(b),a),None
            if op2==1: return '%s %s,%s'%(n,R(b),R(a)),None
            return 'cls4.%d.%d %s,%s'%(op1,op2,R(b),R(a)),None
    if cls==5:
        op1=(w>>10)&7; op2=(w>>8)&3; a=(w>>4)&15; b=w&15
        if op1==0 and op2==0: return 'ld.w %s,%s'%(SREG.get(b,'%%s%d'%b),R(a)),None
        if op1==1 and op2==0: return 'ld.w %s,%s'%(R(b),SREG.get(a,'%%s%d'%a)),None
        if op2==0:
            imm=None
            if len(ext)==1: imm=ext[0]
            elif len(ext)==2: imm=(ext[0]<<13)|ext[1]
            ea='[%s%s]'%(R(a),'' if imm is None else '+0x%x'%imm)
            if op1 in (2,3,4,5): return '%s %s,%d'%(['btst','bclr','bset','bnot'][op1-2],ea,b&7),('bit',['btst','bclr','bset','bnot'][op1-2],a,imm,b&7)
            return ['','','','','','','adc','sbc'][op1]+' %s,%s'%(R(b),R(a)),None
        if op2==1 and op1<4: return ['ld.b','ld.ub','ld.h','ld.uh'][op1]+' %s,%s'%(R(b),R(a)),None
        if op2==2: return ['mlt.h','mltu.h','mlt.w','mltu.w','div0s','div0u','div1','div2s'][op1]+' %s,%s'%(R(b),R(a)),None
        return 'cls5.%d.%d %s,%s'%(op1,op2,R(b),R(a)),None
    if cls==6: return 'ext 0x%x'%(w&0x1fff),('ext',w&0x1fff)
    return '.word 0x%04x'%w,None

def run(data,start,end,base=0):
    out=[];ext=[]
    for off in range(start,end,2):
        w=data[off]|data[off+1]<<8; pc=base+off
        txt,info=dis(w,pc,ext)
        out.append((pc,w,txt,info,tuple(ext)))
        if info and info[0]=='ext': ext=(ext+[info[1]])[-2:]
        else: ext=[]
    return out
if __name__=='__main__':
    d=open(sys.argv[1],'rb').read()
    s=int(sys.argv[2],0) if len(sys.argv)>2 else 0
    e=int(sys.argv[3],0) if len(sys.argv)>3 else len(d)
    b=int(sys.argv[4],0) if len(sys.argv)>4 else 0
    for pc,w,t,i,x in run(d,s,e,b): print('%08x: %04x  %s'%(pc,w,t))
