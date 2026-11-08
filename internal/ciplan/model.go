package ciplan

import (
	"sync"
)

type Component struct {
	Inputs  []string `json:"inputs"`
	Depends []string `json:"depends"`
}

type Target struct {
	Go         []string `json:"go"`
	Components []string `json:"components"`
	Inputs     []string `json:"inputs"`
	Programs   []string `json:"programs,omitempty"`
	Compiler   bool     `json:"compiler"`
}

type Filter struct {
	Paths []string `json:"paths"`
}

type Config struct {
	Version              int                            `json:"version"`
	Global               []string                       `json:"global"`
	Components           map[string]Component           `json:"components"`
	Targets              map[string]Target              `json:"targets"`
	OpaqueTests          map[string]map[string][]string `json:"opaqueTests"`
	HostIndependentUnits []string                       `json:"hostIndependentUnits"`
}

type Lint struct {
	Go         bool `json:"go"`
	Fern       bool `json:"fern"`
	Compiler   bool `json:"compiler"`
	Generators bool `json:"generators"`
	Cache      bool `json:"cache"`
}

type Plan struct {
	Version            int                 `json:"version"`
	Full               bool                `json:"full"`
	Reason             string              `json:"reason"`
	Components         []string            `json:"components"`
	Changed            []string            `json:"changed"`
	Lanes              map[string]bool     `json:"lanes"`
	UnitPackages       []string            `json:"unitPackages"`
	UnitGroups         []string            `json:"unitGroups"`
	UnitRepositoryOnly bool                `json:"unitRepositoryOnly"`
	UnitHosts          string              `json:"unitHosts"`
	NeedsGNU           bool                `json:"needsGNU"`
	Tests              map[string][]string `json:"tests"`
	DarwinTests        map[string][]string `json:"darwinTests"`
	MacosGroups        []string            `json:"macosGroups"`
	MacosFull          bool                `json:"macosFull"`
	Lint               Lint                `json:"lint"`
}

type declaration struct {
	inputs map[string]bool
	refs   map[string]bool
}

type goPackage struct {
	files       []string
	imports     map[string]bool
	testImports map[string]bool
	tests       map[string][]string
	decls       map[string]*declaration
	testOnly    map[string]bool
}

type Graph struct {
	Root        string
	module      string
	Config      Config
	files       map[string]bool
	text        map[string]string
	packages    map[string]*goPackage
	decls       map[string]*declaration
	fern        map[string][]string
	fernReverse map[string][]string
	reverse     map[string][]string
	special     map[string]bool
	platform    sync.Map
}
