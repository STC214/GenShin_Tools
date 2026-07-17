// Package game provides read-only discovery and inspection of Genshin Impact installations.
package game

import "fmt"

type Server uint8

const (
	ServerUnknown Server = iota
	ServerCNOfficial
	ServerCNBilibili
	ServerGlobal
)

func (s Server) String() string {
	switch s {
	case ServerCNOfficial:
		return "国服官服"
	case ServerCNBilibili:
		return "国服 B 服"
	case ServerGlobal:
		return "国际服"
	default:
		return "未知"
	}
}

type Candidate struct {
	Root       string
	Executable string
	ExeName    string
	ConfigPath string
	Version    string
	Server     Server
}

type Discovery struct {
	Candidates []Candidate
	Warnings   []string
}

type AmbiguousError struct {
	Root  string
	Count int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("game directory %q contains %d candidate executables", e.Root, e.Count)
}

var DefaultExecutableNames = []string{"YuanShen.exe", "GenshinImpact.exe"}
