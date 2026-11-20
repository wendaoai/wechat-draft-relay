package wechatdraft

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/gif"
	"io"
)

func validateGIF(reader io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(reader, 1_000_000))
	if err != nil || len(data) >= 1_000_000 || !boundedGIFFrames(data) {
		return errors.New("invalid GIF or frame budget exceeded")
	}
	_, err = gif.DecodeAll(bytes.NewReader(data))
	return err
}

// Keep the desktop's 200 frame / 48 million frame pixel budget before DecodeAll.
func boundedGIFFrames(data []byte) bool {
	if len(data) < 13 {
		return false
	}
	index := 13
	if data[10]&0x80 != 0 {
		index += 3 * (1 << (1 + (data[10] & 7)))
	}
	frames, pixels := 0, int64(0)
	skipBlocks := func() bool {
		for index < len(data) {
			size := int(data[index])
			index++
			if size == 0 {
				return true
			}
			index += size
		}
		return false
	}
	for index < len(data) {
		kind := data[index]
		index++
		switch kind {
		case 0x3b:
			return frames > 0 && index == len(data)
		case 0x21:
			// Extension label, followed by size-prefixed blocks.
			index++
			if !skipBlocks() {
				return false
			}
		case 0x2c:
			if index+9 > len(data) {
				return false
			}
			width := int64(binary.LittleEndian.Uint16(data[index+4:]))
			height := int64(binary.LittleEndian.Uint16(data[index+6:]))
			frames++
			pixels += width * height
			if width == 0 || height == 0 || frames > 200 || pixels > 48_000_000 {
				return false
			}
			packed := data[index+8]
			index += 9
			if packed&0x80 != 0 {
				index += 3 * (1 << (1 + (packed & 7)))
			}
			index++ // LZW minimum code size.
			if !skipBlocks() {
				return false
			}
		default:
			return false
		}
	}
	return false
}
