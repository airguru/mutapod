package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func DisplayVersion() string {
	if strings.TrimSpace(Version) == "" {
		return "dev"
	}
	return Version
}

func Summary() string {
	return fmt.Sprintf("mutapod %s (%s/%s)", DisplayVersion(), runtime.GOOS, runtime.GOARCH)
}
