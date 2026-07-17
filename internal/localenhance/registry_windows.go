package localenhance

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

type NativeRegistry struct{}

func (NativeRegistry) Read(name string) (RegistryValue, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, genshinRegistryPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return RegistryValue{}, nil
	}
	if err != nil {
		return RegistryValue{}, err
	}
	defer key.Close()
	size, kind, err := key.GetValue(name, nil)
	if errors.Is(err, registry.ErrNotExist) {
		return RegistryValue{}, nil
	}
	if err != nil && !errors.Is(err, registry.ErrShortBuffer) {
		return RegistryValue{}, err
	}
	data := make([]byte, size)
	n, kind, err := key.GetValue(name, data)
	if err != nil {
		return RegistryValue{}, err
	}
	return RegistryValue{Exists: true, Kind: kind, Data: data[:n]}, nil
}

func (NativeRegistry) Write(name string, value RegistryValue) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, genshinRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !value.Exists {
		err := key.DeleteValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	switch value.Kind {
	case registry.DWORD:
		if len(value.Data) != 4 {
			return errors.New("DWORD registry backup has invalid size")
		}
		return key.SetDWordValue(name, binary.LittleEndian.Uint32(value.Data))
	case registry.BINARY:
		return key.SetBinaryValue(name, value.Data)
	default:
		return fmt.Errorf("refuse to write unsupported registry type %d", value.Kind)
	}
}
