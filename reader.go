package zstd

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"

	"github.com/cespare/xxhash"
)

// limits
const (
	limitFrameContentSize = 8 << 20 // 8MB
)

const (
	frameMagicNumber = 0xFD2FB528
)

func NewReader(r io.Reader) io.Reader {
	return &reader{
		br:     bufio.NewReader(r),
		window: make([]byte, 4096),
	}
}

type reader struct {
	br *bufio.Reader

	window  []byte
	decpos  uint
	readpos uint
}

func (r *reader) littleEndian(size uint8) (uint64, error) {
	var buf [8]byte
	_, err := r.br.Read(buf[:size])
	val := binary.LittleEndian.Uint64(buf[:])
	return val, err
}

func (r *reader) readAll(p []byte) error {
	_, err := r.br.Read(p)
	return err
}

func (r *reader) Read(p []byte) (int, error) {
	var err error
	for (r.readpos+uint(len(p))) >= r.decpos && err == nil {
		err = r.decodeFrame()
	}
	n := copy(p, r.window[r.readpos:r.decpos])
	return n, err
}

func (r *reader) decodeFrame() error {
	// frame magic number
	magic, err := r.littleEndian(4)
	if err != nil {
		return err
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

	singleSegment := (frameHeader >> 5) & 1
	if singleSegment != 0 {
		fcsFieldSize = 1
	}

	reservedBit := (frameHeader >> 3) & 1
	if reservedBit != 0 {
		return fmt.Errorf("zstd frame reserved bit was set")
	}

	contChecksum := (frameHeader >> 2) & 1

	dictIDFlag := frameHeader & 3
	dictIDFieldSize := dictIDFieldSizes[dictIDFlag]

	if singleSegment == 0 {
		// window descriptor
		panic("TODO")
	}

	// dictionary ID
	dictID, err := r.littleEndian(dictIDFieldSize)
	if err != nil {
		return err
	}
	_ = dictID

	frameContSize, err := r.littleEndian(fcsFieldSize)
	if err != nil {
		return err
	}
	if fcsFieldSize == 2 {
		frameContSize += 256
	}

	if frameContSize > limitFrameContentSize {
		panic("zstd frame content size is too big")
	}
	decstart := r.decpos
	for {
		if err := r.decodeBlock(); err == errLastBlock {
			break
		} else if err != nil {
			return err
		}
	}

	if contChecksum == 1 {
		data := r.window[decstart:r.decpos]
		wantSum, err := r.littleEndian(4)
		if err != nil {
			return err
		}
		gotSum := xxhash.Sum64(data) & 0xFFFFFFFF
		if wantSum != gotSum {
			return fmt.Errorf("frame xxhash mismatch")
		}
	}
	return nil
}

var errLastBlock = fmt.Errorf("last block")

func (r *reader) decodeBlock() error {
	// block header
	blockHeader, err := r.littleEndian(3)
	if err != nil {
		return err
	}
	lastBlock := blockHeader & 1

	blockType := (blockHeader >> 1) & 3

	blockSize := blockHeader >> 3
	// TODO: block size limits
	switch blockType {
	case blockTypeRaw:
		err := r.readAll(r.window[r.decpos : r.decpos+uint(blockSize)])
		if err != nil {
			return err
		}
		r.decpos += uint(blockSize)
	case blockTypeCompressed:
		b, err := r.br.ReadByte()
		if err != nil {
			return err
		}
		litBlockType := b & 3
		litSectionSize := uint64(1)
		var stream []byte
		switch litBlockType {
		case litBlockTypeRaw:
			// literals section
			sizeFormat := (b >> 2) & 1
			if sizeFormat == 1 {
				panic("TODO")
			}
			regSize := b >> 3
			stream = make([]byte, regSize)
			if err := r.readAll(stream); err != nil {
				return err
			}
			litSectionSize += uint64(regSize)
		default:
			panic("unsupported lit block type")
		}
		// sequences section
		seqSectionSize := blockSize - litSectionSize
		seqBs := make([]byte, seqSectionSize)
		if err := r.readAll(seqBs); err != nil {
			return err
		}
		numSeq := uint64(0)
		if b0 := uint64(seqBs[0]); b0 < 128 {
			numSeq = b0
			seqBs = seqBs[1:]
		} else if b0 < 255 {
			b1 := uint64(seqBs[1])
			numSeq = ((b0 - 128) << 8) + b1
			seqBs = seqBs[2:]
		} else if b0 == 255 {
			b1 := uint64(seqBs[1])
			b2 := uint64(seqBs[2])
			numSeq = b1 + (b2 << 8) + 0x7F00
			seqBs = seqBs[3:]
		}
		if numSeq == 0 {
			panic("TODO: sequence section stops")
		}
		b0 := seqBs[0]
		seqBs = seqBs[1:]
		reserved := b0 & 3
		if reserved != 0 {
			panic("symbol compression modes had a non-zero reserved field")
		}

		litLengthTable := r.readFSETable(b0>>6,
			&litLengthCodeDefaultTable)
		offsetTable := r.readFSETable((b0>>4)&3,
			&offsetCodeDefaultTable)
		matchLengthTable := r.readFSETable((b0>>2)&3,
			&matchLengthDefaultTable)

		bitr := backwardBitReader{rem: seqBs}
		bitr.skipPadding()
		litLengthState := bitr.read(litLengthTable.accLog)
		offsetState := bitr.read(offsetTable.accLog)
		matchLengthState := bitr.read(matchLengthTable.accLog)

		for i := uint64(0); i < numSeq; i++ {
			offsetCode := offsetTable.symbol[offsetState]
			offset := (1 << offsetCode) + bitr.read(offsetCode)

			matchLengthCode := matchLengthTable.symbol[matchLengthState]
			matchLength := uint64(matchLengthBaselines[matchLengthCode]) +
				bitr.read(matchLengthExtraBits[matchLengthCode])

			litLengthCode := litLengthTable.symbol[litLengthState]
			litLength := uint64(litLengthBaselines[litLengthCode]) +
				bitr.read(litLengthExtraBits[litLengthCode])

			// sequence execution
			copy(r.window[r.decpos:], stream[:litLength])
			r.decpos += uint(litLength)
			// match_length
			switch offset {
			case 1:
				for n := uint64(0); n < matchLength; n += litLength {
					length := litLength
					if n+litLength >= matchLength {
						length = uint64(len(stream))
					}
					copy(r.window[r.decpos:], stream[:length])
					r.decpos += uint(length)
				}
			default:
				panic("TODO: unimplemented offset")
			}
		}
		if !bitr.empty() {
			return fmt.Errorf("sequence bitstream was corrupted")
		}
	default:
		panic("unsupported block type")
	}
	if lastBlock == 1 {
		return errLastBlock
	}
	return nil
}

func (r *reader) readFSETable(mode byte, predefined *fseTable) *fseTable {
	switch mode {
	case compModePredefined:
		return predefined
	default:
		panic("unsupported compression type")
	}
}

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

func (b *backwardBitReader) read(n uint8) uint64 {
	res := uint64(0)
	for i := uint8(0); i < n; i++ {
		if b.curbits == 0 {
			b.advance()
		}
		bit := b.cur >> 7
		b.cur <<= 1
		b.curbits--

		if bit != 0 {
			res |= 1 << ((n - 1) - i)
		}
	}
	return res
}
