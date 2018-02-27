package zstd

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash"
	"io"
	"math/bits"

	"github.com/cespare/xxhash"
)

// limits
const (
	minWindowSize = 1 << 10 // 1KiB
	maxWindowSize = 8 << 20 // 8MiB

	maxBlockSize = 128 << 10 // 128KiB
)

// magic numbers
const (
	frameMagicNumber = 0xFD2FB528

	skipFrameMagicStart = 0x184D2A50
	skipFrameMagicEnd   = 0x184D2A5F
)

// NewReader returns a Reader that can be used to read uncompressed
// bytes from r.
//
// For the time being, a bufio.Reader is always used on the input
// reader.
func NewReader(r io.Reader) io.Reader {
	return &reader{
		br: bufio.NewReader(r),
	}
}

type reader struct {
	br *bufio.Reader

	midFrame      bool
	frameDecoded  uint64
	frameContSize uint64

	midSequence bool
	blockSize   uint
	isLastBlock bool
	stream      []byte
	numSeq      uint64

	litLengthState   uint
	offsetState      uint
	matchLengthState uint
	litLengthTable   *fseTable
	offsetTable      *fseTable
	matchLengthTable *fseTable

	seqBitReader   backwardBitReader
	seqLitLength   uint
	seqOffset      uint
	seqMatchLength uint
	seqProgress    uint

	window     []byte
	windowSize uint
	decpos     uint
	readpos    uint

	hash    hash.Hash64
	hashing bool
}

// littleEndian reads a little-endian unsigned integer of size bytes.
func (r *reader) littleEndian(size uint8) (uint64, error) {
	var buf [8]byte
	err := r.readFull(buf[:size])
	val := binary.LittleEndian.Uint64(buf[:])
	return val, err
}

// readFull fills all of p with read bytes.
func (r *reader) readFull(p []byte) error {
	_, err := io.ReadFull(r.br, p)
	return err
}

// discard discards exactly size bytes.
func (r *reader) discard(size uint32) error {
	n, err := r.br.Discard(int(size))
	if uint32(n) < size && err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func (r *reader) Read(p []byte) (int, error) {
	err := r.decodeAtLeast(uint(len(p)))
	n := copy(p, r.window[r.readpos:r.decpos])
	r.readpos += uint(n)
	return n, err
}

func (r *reader) decodeAtLeast(size uint) error {
	if size > r.windowSize {
		size = r.windowSize
	}
	for r.readpos+size >= r.decpos {
		if !r.midFrame {
			if err := r.decodeFrameHeader(); err != nil {
				return err
			}
			if size > r.windowSize {
				size = r.windowSize
			}
			continue
		}
		if r.midSequence {
			if err := r.decodeSequences(); err != nil && err != io.EOF {
				return err
			}
			continue
		}
		if err := r.decodeBlockHeader(); err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}

func (r *reader) decoded(data []byte) {
	r.decpos += uint(copy(r.window[r.decpos:], data))
	if r.hashing {
		r.hash.Write(data)
	}
}

// decodeFrameHeader decodes the magic number and header of a zstd
// frame. An error is returned if the frame was malformed, illegal, or
// missing bytes.
func (r *reader) decodeFrameHeader() error {
	// frame magic number
	magic, err := r.littleEndian(4)
	if err != nil {
		return err
	}
	if magic >= skipFrameMagicStart && magic <= skipFrameMagicEnd {
		skipSize, err := r.littleEndian(4)
		if err != nil {
			return fmt.Errorf("missing skippable frame size")
		}
		if err := r.discard(uint32(skipSize)); err != nil {
			return fmt.Errorf("missing skippable frame data")
		}
		return nil
	}
	if magic != frameMagicNumber {
		return fmt.Errorf("not a zstd frame")
	}

	// frame header
	frameHeader, err := r.br.ReadByte()
	if err != nil {
		return fmt.Errorf("missing frame header")
	}
	fcsFlag := frameHeader >> 6
	fcsFieldSize := fcsFieldSizes[fcsFlag]

	singleSegment := frameHeader >> 5 & 1
	if fcsFlag == 0 && singleSegment != 0 {
		fcsFieldSize = 1
	}

	r.frameContSize, err = r.littleEndian(fcsFieldSize)
	if err != nil {
		return fmt.Errorf("missing frame content size")
	}
	if fcsFieldSize == 2 {
		r.frameContSize += 256
	}
	r.frameDecoded = 0

	r.windowSize = minWindowSize
	if singleSegment == 0 {
		// window descriptor
		b, err := r.br.ReadByte()
		if err != nil {
			return fmt.Errorf("missing frame window descriptor")
		}
		exponent := uint(b >> 3)
		mantissa := uint(b & 7)

		windowLog := 10 + exponent
		windowBase := uint(1 << windowLog)
		windowAdd := (windowBase / 8) * mantissa
		r.windowSize = windowBase + windowAdd
	} else if r.frameContSize > uint64(r.windowSize) {
		r.windowSize = uint(r.frameContSize)
	}

	if r.windowSize > maxWindowSize {
		return fmt.Errorf("zstd window size is too big")
	}

	if r.window == nil {
		// 3 times windowSize, so that we can slide windowSize
		// bytes to the left when we're past 2/3rds of the
		// window buffer.
		r.window = make([]byte, 3*r.windowSize)
	}

	reservedBit := frameHeader >> 3 & 1
	if reservedBit != 0 {
		return fmt.Errorf("zstd frame reserved bit was set")
	}

	r.hashing = frameHeader>>2&1 == 1

	dictIDFlag := frameHeader & 3
	dictIDFieldSize := dictIDFieldSizes[dictIDFlag]
	if dictIDFieldSize > 0 {
		// dictionary ID
		dictID, err := r.littleEndian(dictIDFieldSize)
		if err != nil {
			return err
		}
		_ = dictID
		panic("TODO: dictionaries")
	}

	if r.hashing {
		if r.hash == nil {
			r.hash = xxhash.New()
		} else {
			r.hash.Reset()
		}
	}
	r.midFrame = true
	return nil
}

// decodeBlock decodes an entire zstd block within a frame. An error is
// returned if the block was malformed, illegal, or missing bytes.
func (r *reader) decodeBlockHeader() error {
	// block header
	blockHeader, err := r.littleEndian(3)
	if err != nil {
		return fmt.Errorf("missing block header")
	}
	r.isLastBlock = blockHeader & 1 == 1

	blockType := blockHeader >> 1 & 3

	r.blockSize = uint(blockHeader >> 3)
	if r.blockSize > r.windowSize {
		return fmt.Errorf("block size is larger than window size")
	}
	if r.blockSize > maxBlockSize {
		return fmt.Errorf("block size is larger than 128KiB")
	}

	if r.decpos > r.windowSize*2 {
		// slide window left by windowSize
		copy(r.window[:r.windowSize], r.window[r.windowSize:])
		r.decpos -= r.windowSize
		r.readpos -= r.windowSize
	}

	// TODO: block size limits
	switch blockType {
	case blockTypeRaw:
		target := r.window[r.decpos : r.decpos+r.blockSize]
		if err := r.readFull(target); err != nil {
			return fmt.Errorf("missing raw block content")
		}
		r.decoded(target)
	case blockTypeRLE:
		b, err := r.br.ReadByte()
		if err != nil {
			return err
		}
		data := []byte{b}
		// TODO: ridiculously slow
		for i := uint(0); i < r.blockSize; i++ {
			r.decoded(data)
		}
	case blockTypeCompressed:
		return r.decodeBlockCompressed()
	default: // blockTypeReserved
		return fmt.Errorf("reserved block type found")
	}
	return r.endBlock()
}

func (r *reader) endBlock() error {
	r.frameDecoded += uint64(r.blockSize)
	if !r.isLastBlock {
		return nil
	}
	r.midFrame = false
	if r.frameDecoded != r.frameContSize {
		// TODO: fix this
	}
	if r.hashing {
		wantSum, err := r.littleEndian(4)
		if err != nil {
			return fmt.Errorf("missing frame xxhash")
		}
		gotSum := r.hash.Sum64() & 0xFFFFFFFF
		if wantSum != gotSum {
			return fmt.Errorf("frame xxhash mismatch")
		}
	}
	return nil
}

// decodeBlockCompressed is decoupled from decodeBlock to handle
// compressed blocks, the most complex type of them.
func (r *reader) decodeBlockCompressed() error {
	b, err := r.br.ReadByte()
	if err != nil {
		return err
	}
	litBlockType := b & 3
	litSectionSize := uint(1)
	switch litBlockType {
	case litBlockTypeRaw:
		// literals section
		sizeFormat := b >> 2 & 3
		regSize := uint(b >> 3)
		switch sizeFormat {
		case 0, 2: // 00, 10; 1 byte
		case 1: // 01; 2 bytes
			litSectionSize++
			regSize >>= 1
			b, err := r.br.ReadByte()
			if err != nil {
				return err
			}
			regSize |= uint(b << 4)
		case 3: // 11; 3 bytes
			panic("TODO: Size_Format 11")
		}
		litSectionSize += regSize
		r.stream = make([]byte, regSize)
		if err := r.readFull(r.stream); err != nil {
			return err
		}
	default:
		panic("unimplemented lit block type")
	}
	// sequences section
	seqSectionSize := r.blockSize - litSectionSize
	seqBs := make([]byte, seqSectionSize)
	if err := r.readFull(seqBs); err != nil {
		return err
	}
	r.numSeq = uint64(0)
	if b0 := uint64(seqBs[0]); b0 < 128 {
		r.numSeq = b0
		seqBs = seqBs[1:]
	} else if b0 < 255 {
		b1 := uint64(seqBs[1])
		r.numSeq = (b0-128)<<8 + b1
		seqBs = seqBs[2:]
	} else if b0 == 255 {
		b1 := uint64(seqBs[1])
		b2 := uint64(seqBs[2])
		r.numSeq = b1 + b2<<8 + 0x7F00
		seqBs = seqBs[3:]
	}
	if r.numSeq == 0 {
		panic("TODO: sequence section stops")
	}
	b0 := seqBs[0]
	seqBs = seqBs[1:]
	reserved := b0 & 3
	if reserved != 0 {
		return fmt.Errorf("symbol compression modes had a non-zero reserved field")
	}

	r.litLengthTable = r.decodeFSETable(b0>>6,
		&litLengthCodeDefaultTable)
	r.offsetTable = r.decodeFSETable(b0>>4&3,
		&offsetCodeDefaultTable)
	r.matchLengthTable = r.decodeFSETable(b0>>2&3,
		&matchLengthDefaultTable)

	r.seqBitReader = backwardBitReader{rem: seqBs}
	r.seqBitReader.skipPadding()
	r.litLengthState = r.seqBitReader.read(r.litLengthTable.accLog)
	r.offsetState = r.seqBitReader.read(r.offsetTable.accLog)
	r.matchLengthState = r.seqBitReader.read(r.matchLengthTable.accLog)
	return r.decodeSequences()
}

func (r *reader) decodeSequences() error {
	for {
		if !r.midSequence {
			offsetCode := r.offsetTable.symbol[r.offsetState]
			r.seqOffset = 1<<offsetCode + r.seqBitReader.read(offsetCode)

			matchLengthCode := r.matchLengthTable.symbol[r.matchLengthState]
			r.seqMatchLength = uint(matchLengthBaselines[matchLengthCode]) +
				r.seqBitReader.read(matchLengthExtraBits[matchLengthCode])

			litLengthCode := r.litLengthTable.symbol[r.litLengthState]
			r.seqLitLength = uint(litLengthBaselines[litLengthCode]) +
				r.seqBitReader.read(litLengthExtraBits[litLengthCode])

			// sequence execution
			r.decoded(r.stream[:r.seqLitLength])
			r.seqProgress = 0
		}
		switch r.seqOffset {
		case 1:
			for ; r.seqProgress < r.seqMatchLength; r.seqProgress++ {
				if r.decpos > r.windowSize*2 {
					if r.readpos < r.windowSize {
						r.midSequence = true
						return nil
					}
					// slide window left by windowSize
					copy(r.window[:r.windowSize], r.window[r.windowSize:])
					r.decpos -= r.windowSize
					r.readpos -= r.windowSize
				}
				r.decoded(r.window[r.decpos-1:r.decpos])
			}
			r.midSequence = false
		case 2, 3:
			panic("TODO: unimplemented offset")
		default:
			start := r.decpos - (r.seqOffset - 3)
			chunk := r.seqMatchLength
			if max := r.seqOffset - 3; chunk > max {
				chunk = max
			}
			end := start + r.seqMatchLength
			for start < end {
				next := start + chunk
				if next > end {
					next = end
				}
				r.decoded(r.window[start:next])
				start = next
			}
		}
		r.stream = r.stream[r.seqLitLength:]
		if r.numSeq--; r.numSeq == 0 {
			r.decoded(r.stream)
			break
		}

		r.litLengthState = uint(r.litLengthTable.base[r.litLengthState]) +
			r.seqBitReader.read(r.litLengthTable.numBits[r.litLengthState])
		r.matchLengthState = uint(r.matchLengthTable.base[r.matchLengthState]) +
			r.seqBitReader.read(r.matchLengthTable.numBits[r.matchLengthState])
		r.offsetState = uint(r.offsetTable.base[r.offsetState]) +
			r.seqBitReader.read(r.offsetTable.numBits[r.offsetState])
	}
	if !r.seqBitReader.empty() {
		return fmt.Errorf("sequence bitstream was corrupted")
	}
	return r.endBlock()
}

// decodeFSETable decodes a Finite State Entropy table within a
// compressed block.
func (r *reader) decodeFSETable(mode byte, predefined *fseTable) *fseTable {
	switch mode {
	case compModePredefined:
		return predefined
	default:
		panic("unimplemented FSE table compression mode")
	}
}

// backwardBitReader allows access to a backwards bit stream that was
// written in a little-endian fashion. That is, the first bit will be
// the highest bit (after the padding) of the last input byte, and the
// last bit will be the lowest bit of the first byte.
type backwardBitReader struct {
	rem     []byte
	cur     byte
	curbits uint
}

func (b *backwardBitReader) empty() bool {
	return len(b.rem) == 0 && b.curbits == 0
}

func (b *backwardBitReader) advance() {
	b.cur = b.rem[len(b.rem)-1]
	b.rem = b.rem[:len(b.rem)-1]
	b.curbits = 8
}

func (b *backwardBitReader) skipPadding() {
	b.advance()
	// skip padding of 0s plus the first 1
	skip := 1 + uint(bits.LeadingZeros8(b.cur))
	b.cur <<= skip
	b.curbits = 8 - skip
}

func (b *backwardBitReader) read(n uint8) uint {
	// TODO: this can very likely be done more efficiently
	res := uint(0)
	for i := uint8(0); i < n; i++ {
		if b.curbits == 0 {
			b.advance()
		}
		bit := b.cur >> 7
		b.cur <<= 1
		b.curbits--

		if bit != 0 {
			res |= 1 << (n - 1 - i)
		}
	}
	return res
}
