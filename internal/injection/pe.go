package injection

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

type peMetadata struct {
	Architecture string
	IsDLL        bool
	Exports      []string
	Imports      []string
}

func inspectPE(path string) (peMetadata, error) {
	file, err := pe.Open(path)
	if err != nil {
		return peMetadata{}, err
	}
	defer file.Close()
	architecture := fmt.Sprintf("machine-0x%04X", file.Machine)
	if file.Machine == pe.IMAGE_FILE_MACHINE_AMD64 {
		architecture = "amd64"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return peMetadata{}, err
	}
	exports, err := readExports(file, data)
	if err != nil {
		return peMetadata{}, err
	}
	imports, err := readImports(file, data)
	if err != nil {
		return peMetadata{}, err
	}
	return peMetadata{Architecture: architecture, IsDLL: file.Characteristics&pe.IMAGE_FILE_DLL != 0, Exports: exports, Imports: imports}, nil
}

func readImports(file *pe.File, data []byte) ([]string, error) {
	directories, imageBase, err := peDirectories(file)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]string)
	if err := readImportDirectory(file, data, directories[1], 20, func(descriptor []byte) (uint32, bool, error) {
		if allZero(descriptor) {
			return 0, true, nil
		}
		return binary.LittleEndian.Uint32(descriptor[12:16]), false, nil
	}, seen); err != nil {
		return nil, fmt.Errorf("PE import directory: %w", err)
	}
	if err := readImportDirectory(file, data, directories[13], 32, func(descriptor []byte) (uint32, bool, error) {
		if allZero(descriptor) {
			return 0, true, nil
		}
		attributes := binary.LittleEndian.Uint32(descriptor[0:4])
		name := binary.LittleEndian.Uint32(descriptor[4:8])
		if attributes&1 == 0 {
			if imageBase > uint64(^uint32(0)) || uint64(name) < imageBase {
				return 0, false, errors.New("unsupported VA-based delay import name")
			}
			name = uint32(uint64(name) - imageBase)
		}
		return name, false, nil
	}, seen); err != nil {
		return nil, fmt.Errorf("PE delay import directory: %w", err)
	}
	result := make([]string, 0, len(seen))
	for _, name := range seen {
		result = append(result, name)
	}
	sort.Slice(result, func(left, right int) bool {
		return stringLessFold(result[left], result[right])
	})
	return result, nil
}

func peDirectories(file *pe.File) ([16]pe.DataDirectory, uint64, error) {
	var result [16]pe.DataDirectory
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		copy(result[:], header.DataDirectory[:])
		return result, header.ImageBase, nil
	case *pe.OptionalHeader32:
		copy(result[:], header.DataDirectory[:])
		return result, uint64(header.ImageBase), nil
	default:
		return result, 0, errors.New("unsupported PE optional header")
	}
}

func readImportDirectory(file *pe.File, data []byte, directory pe.DataDirectory, descriptorSize uint32, readName func([]byte) (uint32, bool, error), seen map[string]string) error {
	if directory.VirtualAddress == 0 || directory.Size == 0 {
		return nil
	}
	offset, ok := rvaOffset(file, directory.VirtualAddress, uint32(len(data)))
	if !ok {
		return errors.New("directory points outside the file")
	}
	count := directory.Size / descriptorSize
	if count == 0 || count > 4096 {
		return errors.New("descriptor count is outside 1..4096")
	}
	for index := uint32(0); index < count; index++ {
		start := uint64(offset) + uint64(index)*uint64(descriptorSize)
		end := start + uint64(descriptorSize)
		if end > uint64(len(data)) {
			return errors.New("descriptor points outside the file")
		}
		nameRVA, finished, err := readName(data[start:end])
		if err != nil {
			return err
		}
		if finished {
			return nil
		}
		nameOffset, ok := rvaOffset(file, nameRVA, uint32(len(data)))
		if !ok {
			return errors.New("DLL name points outside the file")
		}
		name, err := boundedCString(data, nameOffset, 260)
		if err != nil {
			return fmt.Errorf("DLL name: %w", err)
		}
		if name == "" {
			return errors.New("DLL name is empty")
		}
		key := stringLowerASCII(name)
		if _, exists := seen[key]; !exists {
			seen[key] = name
		}
	}
	return errors.New("directory has no terminating descriptor")
}

func boundedCString(data []byte, offset, limit uint32) (string, error) {
	if offset >= uint32(len(data)) {
		return "", errors.New("string points outside the file")
	}
	end := offset
	for end < uint32(len(data)) && data[end] != 0 && end-offset <= limit {
		end++
	}
	if end == uint32(len(data)) || end-offset > limit {
		return "", errors.New("string is not bounded")
	}
	return string(data[offset:end]), nil
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func stringLowerASCII(value string) string {
	buffer := []byte(value)
	for index, item := range buffer {
		if item >= 'A' && item <= 'Z' {
			buffer[index] = item + ('a' - 'A')
		}
	}
	return string(buffer)
}

func stringLessFold(left, right string) bool {
	return stringLowerASCII(left) < stringLowerASCII(right)
}

func readExports(file *pe.File, data []byte) ([]string, error) {
	var directory pe.DataDirectory
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		directory = header.DataDirectory[0]
	case *pe.OptionalHeader32:
		directory = header.DataDirectory[0]
	default:
		return nil, errors.New("unsupported PE optional header")
	}
	if directory.VirtualAddress == 0 || directory.Size == 0 {
		return nil, nil
	}
	exportOffset, ok := rvaOffset(file, directory.VirtualAddress, uint32(len(data)))
	if !ok || exportOffset+40 > uint32(len(data)) {
		return nil, errors.New("PE export directory is outside the file")
	}
	count := binary.LittleEndian.Uint32(data[exportOffset+24:])
	namesRVA := binary.LittleEndian.Uint32(data[exportOffset+32:])
	if count > 100_000 {
		return nil, errors.New("PE export name count exceeds limit")
	}
	namesOffset, ok := rvaOffset(file, namesRVA, uint32(len(data)))
	if !ok || uint64(namesOffset)+uint64(count)*4 > uint64(len(data)) {
		return nil, errors.New("PE export name table is outside the file")
	}
	result := make([]string, 0, count)
	for index := uint32(0); index < count; index++ {
		nameRVA := binary.LittleEndian.Uint32(data[namesOffset+index*4:])
		nameOffset, ok := rvaOffset(file, nameRVA, uint32(len(data)))
		if !ok {
			return nil, errors.New("PE export name points outside the file")
		}
		name, err := boundedCString(data, nameOffset, 4096)
		if err != nil {
			return nil, fmt.Errorf("PE export name: %w", err)
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func rvaOffset(file *pe.File, rva, fileSize uint32) (uint32, bool) {
	for _, section := range file.Sections {
		// File inspection can only consume raw section bytes. Accepting the
		// virtual zero-fill tail could make a crafted RVA read unrelated bytes
		// that happen to follow the section in the file.
		if rva >= section.VirtualAddress && rva-section.VirtualAddress < section.Size {
			offset := section.Offset + rva - section.VirtualAddress
			return offset, offset < fileSize
		}
	}
	return 0, false
}
