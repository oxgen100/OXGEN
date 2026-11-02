package oxgen

import (
	"context"
	"io"
	"time"
)

type Tool string

const (
	Git       Tool = "git"
	Node      Tool = "node"
	Python    Tool = "python"
	VSCode    Tool = "vscode"
	WSL       Tool = "wsl"
	Codex     Tool = "codex"
	Claude    Tool = "claude"
	WorkBuddy Tool = "workbuddy"
	DeepSeek  Tool = "deepseek-harness"
	Pi        Tool = "pi"
	OpenCode  Tool = "opencode"
)

var coreTools = []Tool{Git, Node, Python, VSCode}
var agentTools = []Tool{Codex, Claude, WorkBuddy, DeepSeek, Pi, OpenCode}

func isAgent(tool Tool) bool {
	for _, t := range agentTools {
		if t == tool {
			return true
		}
	}
	return false
}

func systemPackage(tool Tool) bool { return tool == Git || tool == Python }

func (t Tool) String() string {
	switch t {
	case Node:
		return "Node.js/npm"
	case Python:
		return "Python/pip/venv"
	case WSL:
		return "WSL＋Ubuntu"
	case VSCode:
		return "VS Code"
	case Codex:
		return "Codex CLI"
	case Claude:
		return "Claude Code"
	case WorkBuddy:
		return "WorkBuddy"
	case DeepSeek:
		return "DeepSeek Harness"
	case Pi:
		return "pi"
	case OpenCode:
		return "OpenCode"
	case Git:
		return "Git"
	default:
		return string(t)
	}
}

type Host struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Distro   string `json:"distro,omitempty"`
	Release  string `json:"release,omitempty"`
	Codename string `json:"codename,omitempty"`
}

type Detection struct {
	Tool      Tool     `json:"tool"`
	Installed bool     `json:"installed"`
	Active    bool     `json:"active"`
	Version   string   `json:"version,omitempty"`
	Path      string   `json:"path,omitempty"`
	Source    string   `json:"source,omitempty"`
	Healthy   bool     `json:"healthy"`
	Details   []string `json:"details,omitempty"`
}

type Artifact struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256,omitempty"`
	SHA512    string `json:"sha512,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type Release struct {
	Version   string   `json:"version"`
	Channel   string   `json:"channel"`
	Artifact  Artifact `json:"artifact"`
	Tag       string   `json:"tag,omitempty"`
	Manager   Artifact `json:"manager,omitempty"`
	Package   string   `json:"package,omitempty"`
	Integrity string   `json:"integrity,omitempty"`
}

type Record struct {
	Version      string    `json:"version"`
	Path         string    `json:"path"`
	Root         string    `json:"root"`
	Source       string    `json:"source"`
	UpdatedAt    time.Time `json:"updated_at"`
	Distribution string    `json:"distribution,omitempty"`
	Runtime      string    `json:"runtime,omitempty"`
	Entry        string    `json:"entry,omitempty"`
}

type Result struct {
	Tool    Tool      `json:"tool"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
	Log     string    `json:"log,omitempty"`
	Time    time.Time `json:"time"`
}

type Pending struct {
	Stage        string `json:"stage"`
	Location     string `json:"location,omitempty"`
	Distribution string `json:"distribution,omitempty"`
}

type State struct {
	Schema       int             `json:"schema"`
	Records      map[Tool]Record `json:"records"`
	History      []Result        `json:"history,omitempty"`
	Pending      *Pending        `json:"pending,omitempty"`
	LastDownload string          `json:"last_download,omitempty"`
}

type InstallPlan struct {
	Tool        Tool
	Release     Release
	Current     Detection
	DownloadDir string
	InstallDir  string
	Root        string
	Fixed       bool
	Extra       []string
	Update      bool
	Automatic   bool
}

type Command struct {
	Name        string
	Args        []string
	Dir         string
	Env         []string
	Timeout     time.Duration
	Interactive bool
}

type Runner interface {
	LookPath(string) (string, error)
	Capture(context.Context, Command) (string, error)
	Run(context.Context, Command, io.Writer) error
}
