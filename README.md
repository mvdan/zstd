# zstd

An implementation from scratch of [Zstandard] in Go. It is being
developed following the published [spec].

This is very much a work in progress, so it is not ready for use.
However, testing and contributions are very much welcome.

### Roadmap

This is the current progress of the decoder.

- [x] Zstandard frames
  - [x] Blocks
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
        - [x] Repeated offsets
        - [ ] Other offsets
- [x] Skippable frames

It should be able to handle simple zstd frames, but it will easily break
down with complex ones as it is missing many pieces.

Performance is not a priority until the decoder supports the entire spec
and is well tested and fuzzed.

An encoder will likely be implemented, once a full decoder has been
finished.

### Attributions

Thanks to cespare for his implementation of xxhash: https://github.com/cespare/xxhash

[Zstandard]: https://facebook.github.io/zstd/
[spec]: https://github.com/facebook/zstd/blob/dev/doc/zstd_compression_format.md
