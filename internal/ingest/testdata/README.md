# Ingest test fixtures

Every file here is synthetic: four flat quadrants (top-left red, top-right
green, bottom-left blue, bottom-right white), so a decoded image shows both
its size and its orientation. No file contains a photograph, camera metadata
or personal data.

## Source images and fixtures from the decoder's own encoder

Made with `gen2brain/h265` by `TestGenerateFixtures` in `fixtures_test.go`:

```
INGEST_UPDATE_FIXTURES=1 go test ./internal/ingest -run TestGenerateFixtures
```

| File | What it is |
| --- | --- |
| `quadrants-128x64.png` | 8-bit source, 128x64 |
| `quadrants-256x128.png` | 8-bit source, 256x128 |
| `quadrants16-128x64.png` | 16-bit source, 128x64 |
| `sample.heic` | 8-bit HEIC of `quadrants-128x64.png` |
| `sample-main10.heic` | 10-bit HEIC of `quadrants16-128x64.png` |

The generator rewrites the PNG sources too. After regenerating them, run the
commands below again so that both sets come from the same sources.

The encoder also writes grids (`Tile`), but in v0.2.5 a 64-pixel tiling of
`quadrants-256x128.png` comes out with a grey tile where the source is white.
libheif's `heif-dec` decodes that file the same way, so the defect is in the
encoder, not the decoder, and the grid fixtures come from `heif-enc` instead.

## Fixtures from an independent encoder

Made with libheif's `heif-enc` 1.23.0 (x265 encoder) in a throwaway
container. The tools never enter the repository, CI or the image. From the
repository root:

```
docker run --rm -v "$PWD/internal/ingest/testdata:/work" -w /work alpine:3.23 sh -c '
apk add --no-cache libheif-tools=1.23.0-r0
heif-enc -q 90 -o rotated-heifenc.heic --rotate-cw 90 quadrants-128x64.png
heif-enc -q 90 -o mirrored-heifenc.heic --flip-h quadrants-128x64.png
heif-enc -q 90 -o grid-heifenc.heic --cut-tiles 64 quadrants-256x128.png
heif-enc -q 90 -o grid-rotated-heifenc.heic --cut-tiles 64 --rotate-cw 270 quadrants-256x128.png
heif-enc -q 90 -b 10 -o main10-heifenc.heic quadrants16-128x64.png
'
```

(In Git Bash on Windows, prefix the command with `MSYS_NO_PATHCONV=1`.)

| File | What it is | Shown as (`heif-info`) |
| --- | --- | --- |
| `rotated-heifenc.heic` | `irot` turning the image a quarter turn clockwise | 64x128, angle (ccw) 270 |
| `mirrored-heifenc.heic` | `imir` mirroring left to right | 128x64, mirror horizontal |
| `grid-heifenc.heic` | 4x2 grid of 64x64 tiles | 256x128 |
| `grid-rotated-heifenc.heic` | 2x4 grid with `irot`, as phones store portrait photos | 128x256, angle (ccw) 90 |
| `main10-heifenc.heic` | 10-bit (`heix`) | 128x64 |

When these fixtures were made, libheif's own `heif-dec` decoded every one of
them to the same size and quadrant colours that `TestHEICConverter_DecodesFixturesUpright`
expects from `gen2brain/h265`.

## Fuzz corpus

`FuzzConvertHEIC` is seeded with the `.heic` files above. Run it with:

```
go test ./internal/ingest -run '^$' -fuzz '^FuzzConvertHEIC$' -fuzztime 60s
```

The first run (90 s, about 893,000 inputs) found no failure. When a run
finds one, Go saves the input under `fuzz/FuzzConvertHEIC/`. Keep it there:
`go test` replays every saved input as a regression test.
