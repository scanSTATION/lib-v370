import sys,struct
# usage: jt.py file table_vaddr base_cmd count  (assumes vaddr==file offset for .rodata, verified by caller)
f=open(sys.argv[1],'rb').read()
tab=int(sys.argv[2],16); base=int(sys.argv[3],16); n=int(sys.argv[4])
targets={}
for i in range(n):
    off=struct.unpack_from('<i',f,tab+4*i)[0]
    t=(tab+off)&0xffffffffffffffff
    targets.setdefault(t,[]).append(base+i)
# default = most common
d=max(targets,key=lambda k:len(targets[k]))
print("default target %x (%d entries)"%(d,len(targets[d])))
for t,cs in sorted(targets.items(),key=lambda kv:kv[1][0]):
    if t==d: continue
    print("%x <- %s"%(t,' '.join("%02x(%s)"%(c,chr(c) if 32<c<127 else '.') for c in cs)))
