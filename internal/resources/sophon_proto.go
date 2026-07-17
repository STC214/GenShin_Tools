package resources

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

type sophonFile struct {
	Path     string
	Chunks   []sophonChunk
	Folder   bool
	Size     int64
	Checksum string
}

type sophonChunk struct {
	ID             string
	Checksum       string
	Offset         int64
	CompressedSize int64
	Size           int64
	Metadata64     uint64
	MetadataBytes  []byte
}

func parseSophonManifest(data []byte) ([]sophonFile, error) {
	var files []sophonFile
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		if number != 1 || kind != protowire.BytesType {
			return nil, fmt.Errorf("unknown manifest protobuf field %d/%d", number, kind)
		}
		message, n := protowire.ConsumeBytes(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		file, err := parseSophonFile(message)
		if err != nil {
			return nil, fmt.Errorf("parse manifest file %d: %w", len(files), err)
		}
		files = append(files, file)
		data = data[n:]
	}
	if len(files) == 0 {
		return nil, errors.New("Sophon manifest contains no files")
	}
	return files, nil
}

func parseSophonFile(data []byte) (sophonFile, error) {
	var result sophonFile
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return result, protowire.ParseError(n)
		}
		data = data[n:]
		switch number {
		case 1, 5:
			if kind != protowire.BytesType {
				return result, errors.New("invalid file string wire type")
			}
			value, consumed := protowire.ConsumeString(data)
			if consumed < 0 {
				return result, protowire.ParseError(consumed)
			}
			if number == 1 {
				result.Path = value
			} else {
				result.Checksum = value
			}
			data = data[consumed:]
		case 2:
			if kind != protowire.BytesType {
				return result, errors.New("invalid chunk wire type")
			}
			value, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return result, protowire.ParseError(consumed)
			}
			chunk, err := parseSophonChunk(value)
			if err != nil {
				return result, err
			}
			result.Chunks = append(result.Chunks, chunk)
			data = data[consumed:]
		case 3, 4:
			if kind != protowire.VarintType {
				return result, errors.New("invalid file integer wire type")
			}
			value, consumed := protowire.ConsumeVarint(data)
			if consumed < 0 || value > 1<<63-1 {
				return result, errors.New("invalid file integer")
			}
			if number == 3 {
				result.Folder = value != 0
			} else {
				result.Size = int64(value)
			}
			data = data[consumed:]
		default:
			return result, fmt.Errorf("unknown file protobuf field %d", number)
		}
	}
	return result, nil
}

func parseSophonChunk(data []byte) (sophonChunk, error) {
	var result sophonChunk
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return result, protowire.ParseError(n)
		}
		data = data[n:]
		switch number {
		case 1, 2:
			if kind != protowire.BytesType {
				return result, errors.New("invalid chunk string wire type")
			}
			value, consumed := protowire.ConsumeString(data)
			if consumed < 0 {
				return result, protowire.ParseError(consumed)
			}
			if number == 1 {
				result.ID = value
			} else {
				result.Checksum = value
			}
			data = data[consumed:]
		case 3, 4, 5, 6:
			if kind != protowire.VarintType {
				return result, errors.New("invalid chunk integer wire type")
			}
			value, consumed := protowire.ConsumeVarint(data)
			if consumed < 0 || (number != 6 && value > 1<<63-1) {
				return result, errors.New("invalid chunk integer")
			}
			switch number {
			case 3:
				result.Offset = int64(value)
			case 4:
				result.CompressedSize = int64(value)
			case 5:
				result.Size = int64(value)
			case 6:
				result.Metadata64 = value
			}
			data = data[consumed:]
		case 7:
			if kind != protowire.BytesType {
				return result, errors.New("invalid chunk metadata wire type")
			}
			value, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 || len(value) > 128 {
				return result, errors.New("invalid chunk metadata size")
			}
			result.MetadataBytes = append(result.MetadataBytes[:0], value...)
			data = data[consumed:]
		default:
			return result, fmt.Errorf("unknown chunk protobuf field %d/%d", number, kind)
		}
	}
	return result, nil
}
