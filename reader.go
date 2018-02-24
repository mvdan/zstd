package zstd

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// limits
const (
	limitFrameContentSize = 8 << 20 // 8MB
)

const (
	frameMagicNumber = 0xFD2FB528
)

func NewReader(r io.Reader) io.Reader {
	return &reader{br: bufio.NewReader(r)}
}

type reader struct {
	br *bufio.Reader
	n  int
}

func (r *reader) littleEndian(size int) (uint64, error) {
	var buf [8]byte
	_, err := r.br.Read(buf[:size])
	val := binary.LittleEndian.Uint64(buf[:])
	return val, err
}

func (r *reader) Read(p []byte) (int, error) {
	// frame magic number
	magic, err := r.littleEndian(4)
	if err != nil {
		return 0, err
	}
	if magic != frameMagicNumber {
		return 0, fmt.Errorf("not a zstd frame")
	}

	// frame header
	frameHeader, err := r.br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("missing frame header")
	}
	fcsFlag := frameHeader >> 6
	fcsFieldSize := fcsFieldSizes[fcsFlag]

	singleSegment := (frameHeader >> 5) & 1
	if singleSegment != 0 {
		fcsFieldSize = 1
	}

	reservedBit := (frameHeader >> 3) & 1
	if reservedBit != 0 {
		return 0, fmt.Errorf("zstd frame reserved bit was set")
	}

	contChecksum := (frameHeader >> 2) & 1
	_ = contChecksum // TODO use it

	dictIDFlag := frameHeader & 3
	dictIDFieldSize := dictIDFieldSizes[dictIDFlag]

	if singleSegment == 0 {
		// window descriptor
		panic("TODO")
	}

	// dictionary ID
	dictID, err := r.littleEndian(dictIDFieldSize)
	if err != nil {
		return r.n, err
	}
	_ = dictID

	frameContSize, err := r.littleEndian(fcsFieldSize)
	if err != nil {
		return r.n, err
	}
	if fcsFieldSize == 2 {
		frameContSize += 256
	}

	if frameContSize > limitFrameContentSize {
		panic("zstd frame content size is too big")
	}

	// block header
	for {
		blockHeader, err := r.littleEndian(3)
		if err != nil {
			return r.n, err
		}
		lastBlock := blockHeader & 1

		blockType := (blockHeader >> 1) & 3

		blockSize := blockHeader >> 3
		// TODO: block size limits
		switch blockType {
		case blockTypeRaw:
			for i := uint64(0); i < blockSize; i++ {
				b, _ := r.br.ReadByte()
				p[r.n] = b
				r.n++
			}
		default:
			panic("unsupported block type")
		}
		if lastBlock != 0 {
			break
		}
	}

	return r.n, io.EOF // TODO fix
}

var fcsFieldSizes = []int{0, 2, 4, 8}

var dictIDFieldSizes = []int{0, 1, 2, 4}

const (
	blockTypeRaw = iota
	blockTypeRLE
	blockTypeCompressed
	blockTypeReserved
)
