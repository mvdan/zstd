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
	minWindowSize = 1 << 10 // 1KB
	maxWindowSize = 8 << 20 // 8MB
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

	midFrame bool

	window  []byte
	decpos  uint
	readpos uint

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
	for r.readpos+size >= r.decpos {
		if !r.midFrame {
			if err := r.decodeFrameHeader(); err != nil {
				return err
			}
			continue
		}
		if err := r.decodeBlock(); err != nil && err != io.EOF {
			return err
		}
	}
	return nil
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

	frameContSize, err := r.littleEndian(fcsFieldSize)
	if err != nil {
		return fmt.Errorf("missing frame content size")
	}
	if fcsFieldSize == 2 {
		frameContSize += 256
	}

	windowSize := uint64(minWindowSize)
	if singleSegment == 0 {
		// window descriptor
		panic("TODO")
	} else if frameContSize > windowSize {
		windowSize = frameContSize
	}

	if windowSize > maxWindowSize {
		return fmt.Errorf("zstd window size is too big")
	}

	if r.window == nil {
		r.window = make([]byte, windowSize)
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
func (r *reader) decodeBlock() error {
	// block header
	blockHeader, err := r.littleEndian(3)
	if err != nil {
		return fmt.Errorf("missing block header")
	}
	lastBlock := blockHeader & 1

	blockType := blockHeader >> 1 & 3

	blockSize := uint(blockHeader >> 3)
	startpos := r.decpos
	// TODO: block size limits
	switch blockType {
	case blockTypeRaw:
		err := r.readFull(r.window[r.decpos : r.decpos+blockSize])
		if err != nil {
			return fmt.Errorf("missing raw block content")
		}
		r.decpos += blockSize
	case blockTypeRLE:
		b, err := r.br.ReadByte()
		if err != nil {
			return err
		}
		for i := uint(0); i < blockSize; i++ {
			r.window[r.decpos+i] = b
		}
		r.decpos += blockSize
	case blockTypeCompressed:
		if err := r.decodeBlockCompressed(blockSize); err != nil {
			return err
		}
	default: // blockTypeReserved
		return fmt.Errorf("reserved block type found")
	}
	if r.hashing {
		r.hash.Write(r.window[startpos:r.decpos])
	}
	if lastBlock == 0 {
		return nil
	}
	r.midFrame = false
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
func (r *reader) decodeBlockCompressed(blockSize uint) error {
	b, err := r.br.ReadByte()
	if err != nil {
		return err
	}
	litBlockType := b & 3
	litSectionSize := uint(1)
	var stream []byte
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
		stream = make([]byte, regSize)
		if err := r.readFull(stream); err != nil {
			return err
		}
	default:
		panic("unimplemented lit block type")
	}
	// sequences section
	seqSectionSize := blockSize - litSectionSize
	seqBs := make([]byte, seqSectionSize)
	if err := r.readFull(seqBs); err != nil {
		return err
	}
	numSeq := uint64(0)
	if b0 := uint64(seqBs[0]); b0 < 128 {
		numSeq = b0
		seqBs = seqBs[1:]
	} else if b0 < 255 {
		b1 := uint64(seqBs[1])
		numSeq = (b0-128)<<8 + b1
		seqBs = seqBs[2:]
	} else if b0 == 255 {
		b1 := uint64(seqBs[1])
		b2 := uint64(seqBs[2])
		numSeq = b1 + b2<<8 + 0x7F00
		seqBs = seqBs[3:]
	}
	if numSeq == 0 {
		panic("TODO: sequence section stops")
	}
	b0 := seqBs[0]
	seqBs = seqBs[1:]
	reserved := b0 & 3
	if reserved != 0 {
		return fmt.Errorf("symbol compression modes had a non-zero reserved field")
	}

	litLengthTable := r.decodeFSETable(b0>>6,
		&litLengthCodeDefaultTable)
	offsetTable := r.decodeFSETable(b0>>4&3,
		&offsetCodeDefaultTable)
	matchLengthTable := r.decodeFSETable(b0>>2&3,
		&matchLengthDefaultTable)

	bitr := backwardBitReader{rem: seqBs}
	bitr.skipPadding()
	litLengthState := bitr.read(litLengthTable.accLog)
	offsetState := bitr.read(offsetTable.accLog)
	matchLengthState := bitr.read(matchLengthTable.accLog)

	for {
		offsetCode := offsetTable.symbol[offsetState]
		offset := 1<<offsetCode + bitr.read(offsetCode)

		matchLengthCode := matchLengthTable.symbol[matchLengthState]
		matchLength := uint(matchLengthBaselines[matchLengthCode]) +
			bitr.read(matchLengthExtraBits[matchLengthCode])

		litLengthCode := litLengthTable.symbol[litLengthState]
		litLength := uint(litLengthBaselines[litLengthCode]) +
			bitr.read(litLengthExtraBits[litLengthCode])

		// sequence execution
		copy(r.window[r.decpos:], stream[:litLength])
		r.decpos += litLength
		switch offset {
		case 1:
			for n := uint(0); n < matchLength; n += 1 {
				r.decpos += uint(copy(r.window[r.decpos:],
					r.window[r.decpos-1:r.decpos]))
			}
		case 2, 3:
			panic("TODO: unimplemented offset")
		default:
			start := r.decpos - (offset - 3)
			chunk := matchLength
			if max := offset - 3; chunk > max {
				chunk = max
			}
			end := start + matchLength
			for start < end {
				next := start + chunk
				if next > end {
					next = end
				}
				r.decpos += uint(copy(r.window[r.decpos:],
					r.window[start:next]))
				start = next
			}
		}
		stream = stream[litLength:]
		if numSeq--; numSeq == 0 {
			r.decpos += uint(copy(r.window[r.decpos:], stream))
			break
		}

		litLengthState = uint(litLengthTable.base[litLengthState]) +
			bitr.read(litLengthTable.numBits[litLengthState])
		matchLengthState = uint(matchLengthTable.base[matchLengthState]) +
			bitr.read(matchLengthTable.numBits[matchLengthState])
		offsetState = uint(offsetTable.base[offsetState]) +
			bitr.read(offsetTable.numBits[offsetState])
	}
	if !bitr.empty() {
		return fmt.Errorf("sequence bitstream was corrupted")
	}
	return nil
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
