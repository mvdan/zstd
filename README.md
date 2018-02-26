# zstd

[![GoDoc](https://godoc.org/mvdan.cc/zstd?status.svg)](https://godoc.org/mvdan.cc/zstd)
[![Build Status](https://travis-ci.org/mvdan/zstd.svg?branch=master)](https://travis-ci.org/mvdan/zstd)

	go get -u mvdan.cc/zstd

An implementation from scratch of [Zstandard] in Go. It is being
developed following the published [spec].

This is very much a work in progress, so it is not ready for use.
However, testing and contributions are very much welcome.

### Roadmap

This is the current progress of the decoder.

- [x] Zstandard frames
  - [x] Raw blocks
  - [x] RLE blocks
  - [x] Compressed blocks
    - [x] Literals section
      - [x] Raw literals block
      - [ ] RLE literals block
      - [ ] Compressed literals block
      - [ ] Treeless literals block
    - [x] Sequences section
      - [x] Predefined mode
      - [ ] RLE mode
      - [ ] Repeat mode
      - [ ] FSE compression mode
    - [x] Sequence execution
      - [x] Repeat offsets
      - [x] Other offsets
  - [x] XXH64 frame content checksum
- [x] Skippable frames
- [ ] Dictionaries

It should be able to handle simple zstd frames, but it will easily break
down with complex ones as it is missing many pieces.

Performance is not a priority until the decoder supports the entire spec
and is well tested and fuzzed.

An encoder will likely be implemented, once a full decoder has been
finished.

### Attributions

Thanks to cespare for his implementation of xxhash: https://github.com/cespare/xxhash

Finally, please note that I am not experienced with compression formats
nor with the techniques involved in implementing them safely and
efficiently. As such, it is likely that this implementation is sub-par
in many aspects, until its code has been reviewed by others.

[Zstandard]: https://facebook.github.io/zstd/
[spec]: https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md
