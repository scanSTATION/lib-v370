# Built-in scan profiles

Each profile is the scan part of a real vendor-app session, turned into a
script for our driver (`v370.ExtractProfile`, format in
`v370/profile.go`). It contains the commands, parameter blocks
(settings, motor tables), the expected reply lengths, and semantic steps
for status polling, live values and data reads. Nothing measured is
stored:

- a write step with `"g"` names a standard table the driver computes
  (`v370/tables.go`: `gamma-1.8` tone curve, `ramp-8` identity table);
- a `"calc"` step names a calibration value the driver computes during
  the scan from the scanner's own calibration reads
  (`v370/calibration.go`: `white-offsets`, `image-levels`, `shading`;
  model in `docs/research/calibration.md`).

| Profile | Source trace | Scan | Image data |
|---|---|---|---|
| `a4-100dpi-color` | `a4-75dpi-poweron` | 100 dpi, 826 × 1168 px | 2 982 040 B |
| `a4-200dpi-color` | `a4-150dpi-color` | 200 dpi, 1653 × 2337 px | 11 820 680 B |
| `a4-300dpi-color` | `a4-300dpi-jpeg` | 300 dpi, 2480 × 3507 px | 28 381 912 B |
| `a4-300dpi-gray` | `a4-300dpi-gray` | 300 dpi, mode 0x0800 | 28 381 912 B (still colour-sized) |
| `user-100x50mm-300dpi` | same name | 300 dpi, 1181 × 591 px | 2 779 064 B |
| `user-50x50mm-600dpi` | same name | 600 dpi, 1181 × 1181 px | 4 338 696 B |
| `user-25x25mm-1200dpi` | same name | 1200 dpi, 1181 × 1181 px | 4 425 104 B |
| `a4-600dpi-color` | same name | 600 dpi, 4961 × 7016 px | 105 787 736 B |
| `user-148x210mm-1200dpi` | same name (174 MB) | A5, 1200 dpi, 6992 × 9921 px | 211 063 672 B |

Regenerate (after a new capture):

```bash
go run ./cmd/v370-usbsim profile -trace testdata/traces/TRACE.jsonl.gz \
    -name NAME -description "..." -out v370/profiles/NAME.json
```

`go test ./v370/` replays every profile against its source trace
through the simulator and requires the recorded image bytes exactly.
