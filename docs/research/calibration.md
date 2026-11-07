# Calibration — how a scan corrects itself

The driver computes the per-scan correction values from the scanner's own
calibration reads (`v370/calibration.go`, profile `calc` steps); no
measured value is stored in a profile. This page is the model behind it.
Markers: **[V]** verified in 17 recordings (75–1200 dpi, A4, A5, A6 and
small areas), **[I]** inferred, **[U]** open.

## Calibration passes (every scan) [V]

Before the image pass the host reads calibration data with `1e 47`:

| Read | Geometry (`1e 41` words 0–2) | Content | Settings |
|---|---|---|---|
| calibration-1 | 16 px (32 at 1200/in), 4 lines | **dark level** of a few pixels | `1e 42` all zero, `1e 43` unity |
| white | scan width at sensor resolution, 16 lines | **white reference** (lamp on; reference strip inside the housing [I]) | `1e 42` offsets from calibration-1 |
| dark (1–3 reads) | same width, 21–64 lines each | **dark reference** (lamp off: `1e 22` byte 9 = 01 [I]) | `1e 42` zero |

All calibration reads are 16 bit (`1e 41` byte 8 bits 0–1 = 00),
pixel-interleaved RGB, little endian, followed by the 8-byte block
trailer. The width is `1e 41` word 1 − word 0 in sensor units of the
current sensor mode (`1e 11`: 4 → 400/in, 3 → 600/in, 2 → 1200/in), e.g.
3584 px at 400/in, 5008 at 600/in, 10016 at 1200/in for A4.

## Values the host computes [V]

Notation per channel c and pixel p: W = mean of the white lines, D = mean
of all dark lines.

| Value | Sent with | Model | Agreement with the recordings |
|---|---|---|---|
| white-pass offsets (3 × 2 words) | `1e 42` words 3–8 before the white read | mean of calibration-1 per channel | within 0–30 |
| image-pass offsets (3 × 2 words) | `1e 42` words 3–8 before the image | min over p of D | exact (±1) in 12 of 16; ≤ 35 otherwise |
| base K (3 words) | `1e 42` words 0–2 before the image | min over the used pixels of W | 60–400 lower than sent (the vendor's white estimate is ≈ 0.3 % higher) |
| shading table | `1e 84` type 5, one u16 per pixel and channel, table width = calibration width | **S = f · (W − K)**, 0 beyond the requested scan width (+ ≈ 1 %) | the rebuilt white reference K + S/f differs by 0.6–1.0 % rms — the noise of a 16-line mean |
| table factor f | `1e 41` (image pass) byte 15, high nibble | 8 → f = 1, 7 → f = 2 (small areas, where 2·(W − K) fits in 16 bits) | all recordings |
| gains | `1e 43` | constant `30982 31434 31552` (twice) | all recordings |
| calibration echo | `1e 31` | the scanner's own `1e 92` reply (already live) | — |

So the scanner reconstructs a per-pixel white reference K + S/f and
subtracts the dark offset; the host only has to average, take minima and
subtract.

The recording made by our own driver (replaying recorded values) shows
why live values matter: its replayed offsets were 180–350 off the dark
level actually measured in that scan, and its shading 1.2 % off.

Not calibration: `1e 84` type 4 uploads (2 B, 768 B, 2 KB) are motor
tables (firmware map: type 4 = motor table), identical for equal speeds;
the 256 B type 9 block is constant; the type 2 tone curve and type 8
identity tables are generated (`v370/tables.go`).

## Implementation

`calibration.go` classifies the reads (first: short dark; first wide:
white; later reads of the same span: dark), averages per pixel and
channel, and takes the minima over the used width (requested width ×
sensor/scan resolution × 1.01). Tests: synthetic data
(`calibration_test.go`), and with private recordings the computed values
are compared with the vendor's (offsets within 40–80, rebuilt white
reference within 1.5 % rms; observed 0.6–1.0 %).

## Open [U]

- The vendor's exact white statistic (explains the 0.3 % and the K
  offset); irrelevant for image quality.

## Real-scanner check (white sheet, 2026-11-07)

Column evenness of a blank white sheet (column means, deviation from a
31-column moving average, rms in 8-bit levels; colour = mean R/G/B):

| Scan | Recorded values (old session) | Computed values |
|---|---|---|
| A4 300 dpi (sensor 400/in) | streaks 0.64–0.86; 240/240/244 | streaks 1.32–1.43 (3 scans); 239/238/247 |
| A4 300 dpi, 64 white lines instead of 16 | — | streaks 1.32–1.42 (no change) |
| A4 600 dpi (sensor 600/in) | streaks 0.77–0.91; 239/245/246 | streaks 0.94–1.00; 239/239/247 |

- Large-scale evenness (left–right fall-off) is the same either way.
- Computed colour balance is consistent across resolutions; the recorded
  tables differ between resolutions (green +6 at 600 dpi).
- The extra streaks at 300 dpi are random per scan, but they are **not**
  the noise of the white average (4× more white lines change nothing).
  They are tied to the 400/in sensor mode with its 4→3 conversion to
  300 dpi [U]. The vendor's table contains a component not explained by
  this scan's white read (≈ the size of a 16-line noise; partly correlated
  with the dark read's pixel pattern in R and B) that helps there [U].
