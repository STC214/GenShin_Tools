// Package localenhance owns reversible local game integrations.
package localenhance

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	HDRStateName        = "WINDOWS_HDR_ON_h3132281285"
	GeneralDataName     = "GENERAL_DATA_h2389025596"
	genshinRegistryPath = `Software\miHoYo\原神`
)

type HDRConfig struct {
	Enabled        bool `json:"enabled"`
	MaxLuminance   int  `json:"maxLuminance"`
	SceneLuminance int  `json:"sceneLuminance"`
	UILuminance    int  `json:"uiLuminance"`
}

func DefaultHDRConfig() HDRConfig {
	return HDRConfig{MaxLuminance: 1000, SceneLuminance: 300, UILuminance: 350}
}

func (c HDRConfig) Normalized() (HDRConfig, error) {
	if c.MaxLuminance < 300 || c.MaxLuminance > 2000 {
		return HDRConfig{}, errors.New("HDR max luminance must be within 300..2000")
	}
	if c.SceneLuminance < 100 || c.SceneLuminance > 500 {
		return HDRConfig{}, errors.New("HDR scene luminance must be within 100..500")
	}
	if c.UILuminance < 150 || c.UILuminance > 550 {
		return HDRConfig{}, errors.New("HDR UI luminance must be within 150..550")
	}
	return c, nil
}

type RegistryValue struct {
	Exists bool   `json:"exists"`
	Kind   uint32 `json:"kind"`
	Data   []byte `json:"data,omitempty"`
}

type HDRSnapshot struct {
	State   RegistryValue `json:"state"`
	General RegistryValue `json:"general"`
}

type RegistryStore interface {
	Read(name string) (RegistryValue, error)
	Write(name string, value RegistryValue) error
}

func ReadHDR(store RegistryStore) (HDRConfig, HDRSnapshot, error) {
	state, err := store.Read(HDRStateName)
	if err != nil {
		return HDRConfig{}, HDRSnapshot{}, err
	}
	general, err := store.Read(GeneralDataName)
	if err != nil {
		return HDRConfig{}, HDRSnapshot{}, err
	}
	snapshot := HDRSnapshot{State: state, General: general}
	config := DefaultHDRConfig()
	if state.Exists {
		if state.Kind != registry.DWORD || len(state.Data) != 4 {
			return HDRConfig{}, snapshot, fmt.Errorf("%s has unexpected registry type or size", HDRStateName)
		}
		config.Enabled = binary.LittleEndian.Uint32(state.Data) == 1
	}
	if general.Exists {
		if general.Kind != registry.BINARY {
			return HDRConfig{}, snapshot, fmt.Errorf("%s has unexpected registry type", GeneralDataName)
		}
		if err := decodeHDRGeneral(general.Data, &config); err != nil {
			return HDRConfig{}, snapshot, err
		}
	}
	return config, snapshot, nil
}

func ApplyHDR(store RegistryStore, config HDRConfig) (HDRSnapshot, error) {
	config, err := config.Normalized()
	if err != nil {
		return HDRSnapshot{}, err
	}
	_, snapshot, err := ReadHDR(store)
	if err != nil {
		return snapshot, err
	}
	return applyHDRSnapshot(store, config, snapshot)
}

func applyHDRSnapshot(store RegistryStore, config HDRConfig, snapshot HDRSnapshot) (HDRSnapshot, error) {
	general, err := encodeHDRGeneral(snapshot.General, config)
	if err != nil {
		return snapshot, err
	}
	stateData := make([]byte, 4)
	if config.Enabled {
		binary.LittleEndian.PutUint32(stateData, 1)
	}
	if err := store.Write(GeneralDataName, RegistryValue{Exists: true, Kind: registry.BINARY, Data: general}); err != nil {
		return snapshot, err
	}
	if err := store.Write(HDRStateName, RegistryValue{Exists: true, Kind: registry.DWORD, Data: stateData}); err != nil {
		return snapshot, errors.Join(err, RestoreHDR(store, snapshot))
	}
	return snapshot, nil
}

func RestoreHDR(store RegistryStore, snapshot HDRSnapshot) error {
	return errors.Join(store.Write(GeneralDataName, snapshot.General), store.Write(HDRStateName, snapshot.State))
}

func decodeHDRGeneral(data []byte, config *HDRConfig) error {
	data = bytes.TrimRight(data, "\x00")
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return fmt.Errorf("decode Genshin HDR JSON: %w", err)
	}
	for name, target := range map[string]*int{"maxLuminosity": &config.MaxLuminance, "scenePaperWhite": &config.SceneLuminance, "uiPaperWhite": &config.UILuminance} {
		if raw, exists := values[name]; exists {
			number, ok := raw.(json.Number)
			if !ok {
				return fmt.Errorf("HDR field %s is not numeric", name)
			}
			value, err := number.Int64()
			if err != nil {
				floatValue, floatErr := number.Float64()
				if floatErr != nil {
					return fmt.Errorf("HDR field %s is invalid", name)
				}
				value = int64(floatValue)
			}
			*target = int(value)
		}
	}
	_, err := config.Normalized()
	return err
}

func encodeHDRGeneral(original RegistryValue, config HDRConfig) ([]byte, error) {
	values := make(map[string]any)
	if original.Exists && len(bytes.Trim(original.Data, "\x00 \r\n\t")) > 0 {
		decoder := json.NewDecoder(strings.NewReader(strings.TrimRight(string(original.Data), "\x00")))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return nil, fmt.Errorf("preserve Genshin settings JSON: %w", err)
		}
	}
	values["maxLuminosity"] = config.MaxLuminance
	values["scenePaperWhite"] = config.SceneLuminance
	values["uiPaperWhite"] = config.UILuminance
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return append(data, 0), nil
}
