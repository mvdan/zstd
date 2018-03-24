# zstd

An implementation from scratch of [Zstandard] in [Wuffs]. It is being
developed following the published [spec].

This is very much a work in progress, so it is not ready for use.

To build a simple `zstd` binary that will use stdin and stdout:

	./build

And to test it with the input/output cases in `testdata`:

	./test

### Why?

Writing a decoder in Wuffs takes more time, but the end result is an
implementation that is safe and can be used in many languages without
linking against C.

For example, that would mean no cgo overhead with Go, and safer code for
languages like Rust. Though that is somewhere in the future - see the
roadmap.

If you're after a zstd implementation that works today, use
https://github.com/DataDog/zstd.

### Roadmap

This is the current progress of the decoder.

- [x] Zstandard frames
  - [x] Raw blocks
  - [ ] RLE blocks
  - [ ] Compressed blocks
    - [ ] Literals section
      - [ ] Raw literals block
      - [ ] RLE literals block
      - [ ] Compressed literals block
      - [ ] Treeless literals block
    - [ ] Sequences section
      - [ ] Predefined mode
      - [ ] RLE mode
      - [ ] Repeat mode
      - [ ] FSE compression mode
    - [ ] Sequence execution
      - [ ] Repeat offsets
      - [ ] Other offsets
  - [ ] XXH64 frame content checksum
- [ ] Skippable frames
- [ ] Dictionaries

These items are required for a stable 1.0 release:

- [ ] Wuffs 1.0 release
- [ ] Go support in Wuffs (generating a Go zstd library)
- [ ] Full zstd decoder implemented

[Zstandard]: https://facebook.github.io/zstd/
[Wuffs]: https://github.com/google/wuffs
[spec]: https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md
