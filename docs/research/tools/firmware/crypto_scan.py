"""Scan payload for crypto constants: SHA/MD5 IVs, CRC tables, AES S-box, big RSA/ECC-looking blobs."""
import sys
d=open(sys.argv[1],'rb').read()

def find_bytes(needle,label):
    hits=[]
    i=d.find(needle)
    while i!=-1:
        hits.append(i); i=d.find(needle,i+1)
    print('%-28s %s'%(label, ['0x%x'%h for h in hits] or 'NOT FOUND'))

# SHA-256 init H0..H7 (big-endian and little-endian)
sha256=[0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19]
# SHA-1 init
sha1=[0x67452301,0xefcdab89,0x98badcfe,0x10325476,0xc3d2e1f0]
# MD5 init (= sha1 first 4 in diff order) and MD5 T sin table first consts
md5=[0x67452301,0xefcdab89,0x98badcfe,0x10325476]
md5T=[0xd76aa478,0xe8c7b756]
# SHA-256 round constants first few
sha256k=[0x428a2f98,0x71374491]

def scan_words(words,label):
    found=[]
    for w in words:
        for en in ('<I','>I'):
            import struct
            b=struct.pack(en,w)
            if b in d:
                found.append('%08x(%s)@0x%x'%(w,en,d.find(b)))
    print('%-28s %s'%(label, found or 'none'))

scan_words(sha256,'SHA-256 IV')
scan_words(sha256k,'SHA-256 K[0..1]')
scan_words(sha1,'SHA-1 IV')
scan_words(md5,'MD5 IV')
scan_words(md5T,'MD5 T[0..1]')

# AES S-box signature bytes
aes=bytes([0x63,0x7c,0x77,0x7b,0xf2,0x6b,0x6f,0xc5,0x30,0x01,0x67,0x2b,0xfe,0xd7,0xab,0x76])
find_bytes(aes,'AES S-box head')
# AES Rcon / Te table marker
find_bytes(bytes([0xc6,0x63,0x63,0xa5]),'AES Te[0] c66363a5')

# CRC32 table: table[1]=0x77073096 (reflected), table[128]=... ; check well-known entries
import struct
crc32_refl_1=struct.pack('<I',0x77073096)
crc32_refl_2=struct.pack('<I',0xee0e612c)
find_bytes(crc32_refl_1,'CRC32 refl tbl[1]')
find_bytes(crc32_refl_2,'CRC32 refl tbl[2]')
crc32_1=struct.pack('<I',0x04c11db7)
find_bytes(crc32_1,'CRC32 poly 04c11db7')
crc16_ccitt=struct.pack('<H',0x1021)
# too common, skip reporting broadly

# High-entropy / big-constant blobs (RSA modulus / ECC): look for long runs with high entropy inside code region
# Report regions of >=64 bytes where entropy ~8 (random-looking) that aren't 0xFF padding
import math
def ent(b):
    if not b: return 0
    from collections import Counter
    c=Counter(b); n=len(b)
    return -sum(v/n*math.log2(v/n) for v in c.values())
print('--- high-entropy 64B windows (entropy>7.0), excluding FF-padding region ---')
step=32
flagged=[]
for i in range(0,len(d)-64,step):
    w=d[i:i+64]
    if w.count(0xff)>40: continue
    e=ent(w)
    if e>7.0:
        flagged.append((i,round(e,2)))
# merge adjacent
if flagged:
    for i,e in flagged[:40]:
        print('  0x%x entropy=%.2f'%(i,e))
    print('  total flagged windows:',len(flagged))
else:
    print('  none')
