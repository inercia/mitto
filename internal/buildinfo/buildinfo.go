// Package buildinfo exposes the identity embedded in the running Mitto binary.
package buildinfo

import (
	"os"
	"runtime/debug"
)

const unknown = "unknown"

// Info identifies the executable and source revision used to build it.
type Info struct {
	Executable string
	Revision   string
	BuildTime  string
	Modified   bool
}

// Read returns the best available identity for the running binary.
func Read() Info {
	return read(os.Executable, debug.ReadBuildInfo)
}

func read(executable func() (string, error), readBuildInfo func() (*debug.BuildInfo, bool)) Info {
	info := Info{Executable: unknown, Revision: unknown, BuildTime: unknown}
	if path, err := executable(); err == nil && path != "" {
		info.Executable = path
	}
	if build, ok := readBuildInfo(); ok {
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				info.Revision = setting.Value
			case "vcs.time":
				info.BuildTime = setting.Value
			case "vcs.modified":
				info.Modified = setting.Value == "true"
			}
		}
	}
	return info
}
